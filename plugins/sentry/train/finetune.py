#!/usr/bin/env python3
"""Fine-tune FunctionGemma 270M on Goblin Sentry answers (desktop tool).

Input: prompt/completion pairs from `run llm-data` (the Sentry plugin
program), so the prompts are exactly the ones the box sends. Output: a
Hugging Face model directory and, with --gguf, a GGUF file for llama.cpp's
llama-server.

  run llm-data -examples examples.jsonl -train train.jsonl -test test.jsonl
  python finetune.py --base functiongemma-270m-it --train train.jsonl --out goblin-ft \
      --gguf goblin-ft.gguf --llama-cpp /path/to/llama.cpp

Loss is on the completion only. Deterministic for the same seed and data
(up to GPU kernel nondeterminism). Needs torch and transformers; a GPU
makes it take minutes instead of hours.
"""
import argparse
import json
import random
import subprocess
import sys
from pathlib import Path

import torch
from transformers import AutoModelForCausalLM, AutoTokenizer


def load_pairs(path):
    with open(path, encoding="utf-8") as f:
        return [json.loads(line) for line in f if line.strip()]


def encode(tok, pair, max_len):
    prompt = tok(pair["prompt"], add_special_tokens=True)["input_ids"]
    completion = tok(pair["completion"], add_special_tokens=False)["input_ids"] + [tok.eos_token_id]
    ids = (prompt + completion)[-max_len:]
    labels = ([-100] * len(prompt) + completion)[-max_len:]
    return ids, labels


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True, help="base model directory (FunctionGemma 270M)")
    ap.add_argument("--train", required=True, help="training pairs (JSONL)")
    ap.add_argument("--out", required=True, help="output model directory")
    ap.add_argument("--epochs", type=int, default=3)
    ap.add_argument("--lr", type=float, default=5e-5)
    ap.add_argument("--batch", type=int, default=8)
    ap.add_argument("--max-len", type=int, default=2048)
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--gguf", help="also write a GGUF file (Q8_0) here")
    ap.add_argument("--llama-cpp", help="llama.cpp checkout (for convert_hf_to_gguf.py)")
    args = ap.parse_args()

    random.seed(args.seed)
    torch.manual_seed(args.seed)
    device = "cuda" if torch.cuda.is_available() else "cpu"
    tok = AutoTokenizer.from_pretrained(args.base)
    model = AutoModelForCausalLM.from_pretrained(args.base, torch_dtype=torch.bfloat16 if device == "cuda" else torch.float32)
    model.to(device)
    model.train()

    data = [encode(tok, p, args.max_len) for p in load_pairs(args.train)]
    print(f"{len(data)} examples on {device}", file=sys.stderr)
    opt = torch.optim.AdamW(model.parameters(), lr=args.lr, weight_decay=0.0)
    steps = args.epochs * ((len(data) + args.batch - 1) // args.batch)
    sched = torch.optim.lr_scheduler.LambdaLR(opt, lambda s: min(1.0, (s + 1) / 20) * max(0.0, 1 - s / steps))
    pad = tok.pad_token_id if tok.pad_token_id is not None else 0
    step = 0
    for epoch in range(args.epochs):
        random.shuffle(data)
        total = 0.0
        for i in range(0, len(data), args.batch):
            batch = data[i : i + args.batch]
            # left padding: the completions end every row, so logits are only
            # needed for the last few positions (the vocabulary is 262k)
            n = max(len(ids) for ids, _ in batch)
            ids = torch.tensor([[pad] * (n - len(x)) + x for x, _ in batch], device=device)
            labels = torch.tensor([[-100] * (n - len(y)) + y for _, y in batch], device=device)
            mask = torch.tensor([[0] * (n - len(x)) + [1] * len(x) for x, _ in batch], device=device)
            pos = (mask.cumsum(-1) - 1).clamp(min=0)
            keep = max(sum(1 for t in y if t != -100) for _, y in batch) + 1
            logits = model(input_ids=ids, attention_mask=mask, position_ids=pos, logits_to_keep=keep).logits
            # logits[:, j] predicts token n-keep+j+1
            target = labels[:, n - keep + 1 :]
            loss = torch.nn.functional.cross_entropy(logits[:, :-1].float().reshape(-1, logits.size(-1)), target.reshape(-1), ignore_index=-100)
            loss.backward()
            torch.nn.utils.clip_grad_norm_(model.parameters(), 1.0)
            opt.step()
            sched.step()
            opt.zero_grad()
            total += loss.item() * len(batch)
            step += 1
        print(f"epoch {epoch + 1}: loss {total / len(data):.4f}", file=sys.stderr)

    out = Path(args.out)
    model.save_pretrained(out, safe_serialization=True)
    # the tokenizer is unchanged: copy the base files as they are (the GGUF
    # converter needs tokenizer.model)
    for name in ("tokenizer.model", "tokenizer.json", "tokenizer_config.json", "special_tokens_map.json",
                 "added_tokens.json", "chat_template.jinja"):
        src = Path(args.base) / name
        if src.exists():
            (out / name).write_bytes(src.read_bytes())
    print(f"saved {out}", file=sys.stderr)

    if args.gguf:
        if not args.llama_cpp:
            sys.exit("--gguf needs --llama-cpp")
        conv = Path(args.llama_cpp) / "convert_hf_to_gguf.py"
        subprocess.run([sys.executable, str(conv), str(out), "--outfile", args.gguf, "--outtype", "q8_0"], check=True)
        print(f"wrote {args.gguf}", file=sys.stderr)


if __name__ == "__main__":
    main()
