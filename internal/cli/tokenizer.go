package cli

import (
	"fmt"
	"strings"
)

// Token represents a parsed token from the CLI input
type Token struct {
	Type  TokenType
	Value string
	Line  int
	Pos   int
}

// TokenType defines the type of a token
type TokenType int

const (
	TokenError TokenType = iota
	TokenConfig
	TokenShow
	TokenEdit
	TokenSet
	TokenUnset
	TokenAppend
	TokenNext
	TokenDelete
	TokenEnd
	TokenString
	TokenEOF
)

// Tokenizer parses CLI input into tokens
type Tokenizer struct {
	input string
	pos   int
	line  int
	col   int
}

// NewTokenizer creates a new tokenizer for the given input
func NewTokenizer(input string) *Tokenizer {
	return &Tokenizer{
		input: input,
		line:  1,
		col:   1,
	}
}

// NextToken returns the next token from the input
func (t *Tokenizer) NextToken() Token {
	if t.pos >= len(t.input) {
		return Token{Type: TokenEOF, Line: t.line, Pos: t.pos}
	}

	// Skip whitespace
	for t.pos < len(t.input) && isSpace(rune(t.input[t.pos])) {
		if t.input[t.pos] == '\n' {
			t.line++
			t.col = 1
		} else {
			t.col++
		}
		t.pos++
	}

	if t.pos >= len(t.input) {
		return Token{Type: TokenEOF, Line: t.line, Pos: t.pos}
	}

	// Check for comments
	if t.pos+1 < len(t.input) && t.input[t.pos] == '#' && t.input[t.pos+1] == ' ' {
		// Skip to end of line
		for t.pos < len(t.input) && t.input[t.pos] != '\n' {
			t.pos++
		}
		return t.NextToken()
	}

	// Check for quoted strings
	if t.input[t.pos] == '"' {
		return t.parseString()
	}

	// Parse tokens by whitespace boundaries
	start := t.pos
	for t.pos < len(t.input) && !isSpace(rune(t.input[t.pos])) && t.input[t.pos] != '"' {
		t.pos++
		t.col++
	}

	value := t.input[start:t.pos]

	// Determine token type by value
	switch value {
	case "config":
		return Token{Type: TokenConfig, Value: value, Line: t.line, Pos: start}
	case "show":
		return Token{Type: TokenShow, Value: value, Line: t.line, Pos: start}
	case "edit":
		return Token{Type: TokenEdit, Value: value, Line: t.line, Pos: start}
	case "set":
		return Token{Type: TokenSet, Value: value, Line: t.line, Pos: start}
	case "unset":
		return Token{Type: TokenUnset, Value: value, Line: t.line, Pos: start}
	case "append":
		return Token{Type: TokenAppend, Value: value, Line: t.line, Pos: start}
	case "next":
		return Token{Type: TokenNext, Value: value, Line: t.line, Pos: start}
	case "delete":
		return Token{Type: TokenDelete, Value: value, Line: t.line, Pos: start}
	case "end":
		return Token{Type: TokenEnd, Value: value, Line: t.line, Pos: start}
	default:
		return Token{Type: TokenString, Value: value, Line: t.line, Pos: start}
	}
}

// parseString parses a quoted string
func (t *Tokenizer) parseString() Token {
	start := t.pos
	t.pos++ // Skip opening quote
	t.col++

	for t.pos < len(t.input) && t.input[t.pos] != '"' {
		if t.input[t.pos] == '\\' {
			t.pos += 2 // Skip escaped character
			t.col += 2
		} else {
			t.pos++
			t.col++
		}
	}

	if t.pos >= len(t.input) {
		return Token{Type: TokenError, Value: "unterminated string", Line: t.line, Pos: start}
	}

	t.pos++ // Skip closing quote
	t.col++

	return Token{Type: TokenString, Value: t.input[start+1 : t.pos-1], Line: t.line, Pos: start}
}

// Tokens returns all tokens from the input
func (t *Tokenizer) Tokens() []Token {
	var tokens []Token
	for {
		token := t.NextToken()
		tokens = append(tokens, token)
		if token.Type == TokenEOF {
			break
		}
	}
	return tokens
}

// Helper function to check if a character is space
func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// Operation represents a single operation to be applied to config
type Operation struct {
	Type    OperationType
	Path    string
	Value   string
	Name    string
	Section string
}

// OperationType defines the type of operation
type OperationType int

const (
	OpSet OperationType = iota
	OpUnset
	OpAppend
	OpDelete
	OpShow
)

// Parser parses CLI commands into operations
type Parser struct {
	tokens []Token
	pos    int
}

// NewParser creates a new parser
func NewParser(tokens []Token) *Parser {
	return &Parser{
		tokens: tokens,
		pos:    0,
	}
}

// Parse parses the tokens into operations
func (p *Parser) Parse() ([]Operation, error) {
	var operations []Operation
	for p.pos < len(p.tokens) {
		token := p.peek()
		switch token.Type {
		case TokenEOF:
			return operations, nil
		case TokenConfig:
			ops, err := p.parseConfig()
			if err != nil {
				return nil, err
			}
			operations = append(operations, ops...)
		case TokenShow:
			ops, err := p.parseShow()
			if err != nil {
				return nil, err
			}
			operations = append(operations, ops...)
		default:
			return nil, fmt.Errorf("unexpected token at position %d: %s", token.Pos, token.Value)
		}
	}
	return operations, nil
}

// parseConfig handles config command and subcommands
func (p *Parser) parseConfig() ([]Operation, error) {
	p.consume(TokenConfig)

	// Get the section path
	sectionTokens := []Token{}
	for p.pos < len(p.tokens) {
		token := p.peek()
		if token.Type == TokenEOF || token.Type == TokenConfig || token.Type == TokenShow ||
			token.Type == TokenEdit || token.Type == TokenDelete || token.Type == TokenEnd {
			break
		}
		sectionTokens = append(sectionTokens, token)
		p.consume(token.Type)
	}

	if len(sectionTokens) == 0 {
		return nil, fmt.Errorf("missing section path after config")
	}

	section := p.tokensToString(sectionTokens)

	// Map section to config path
	configPath, err := mapSectionToPath(section)
	if err != nil {
		return nil, err
	}

	var operations []Operation

	// Parse subcommands within the config block
	for p.pos < len(p.tokens) {
		token := p.peek()
		if token.Type == TokenEnd {
			p.consume(TokenEnd)
			break
		}

		switch token.Type {
		case TokenEdit:
			ops, err := p.parseEdit(configPath)
			if err != nil {
				return nil, err
			}
			operations = append(operations, ops...)
		case TokenShow:
			ops, err := p.parseShow()
			if err != nil {
				return nil, err
			}
			operations = append(operations, ops...)
		case TokenDelete:
			ops, err := p.parseDelete(configPath)
			if err != nil {
				return nil, err
			}
			operations = append(operations, ops...)
		default:
			return nil, fmt.Errorf("unexpected token in config block: %s", token.Value)
		}
	}

	return operations, nil
}

// parseEdit handles edit command
func (p *Parser) parseEdit(configPath string) ([]Operation, error) {
	p.consume(TokenEdit)

	// Get the name or ID
	token := p.peek()
	if token.Type != TokenString {
		return nil, fmt.Errorf("expected name or ID after edit, got %s", token.Value)
	}
	name := p.consume(TokenString).Value

	var operations []Operation

	// Parse subcommands within edit block
	for p.pos < len(p.tokens) {
		token := p.peek()
		if token.Type == TokenNext || token.Type == TokenEnd {
			p.consume(token.Type)
			break
		}

		switch token.Type {
		case TokenSet:
			ops, err := p.parseSet(configPath, name)
			if err != nil {
				return nil, err
			}
			operations = append(operations, ops...)
		case TokenUnset:
			ops, err := p.parseUnset(configPath, name)
			if err != nil {
				return nil, err
			}
			operations = append(operations, ops...)
		case TokenAppend:
			ops, err := p.parseAppend(configPath, name)
			if err != nil {
				return nil, err
			}
			operations = append(operations, ops...)
		case TokenDelete:
			ops, err := p.parseDelete(configPath)
			if err != nil {
				return nil, err
			}
			operations = append(operations, ops...)
		case TokenShow:
			ops, err := p.parseShow()
			if err != nil {
				return nil, err
			}
			operations = append(operations, ops...)
		default:
			return nil, fmt.Errorf("unexpected token in edit block: %s", token.Value)
		}
	}

	return operations, nil
}

// parseSet handles set command
func (p *Parser) parseSet(configPath, name string) ([]Operation, error) {
	p.consume(TokenSet)

	// Get the field name
	token := p.peek()
	if token.Type != TokenString {
		return nil, fmt.Errorf("expected field name after set, got %s", token.Value)
	}
	field := p.consume(TokenString).Value

	// Get the values
	var values []string
	for p.pos < len(p.tokens) {
		token := p.peek()
		if token.Type == TokenNext || token.Type == TokenEnd || token.Type == TokenConfig || token.Type == TokenShow || token.Type == TokenEdit || token.Type == TokenDelete {
			break
		}
		values = append(values, p.consume(TokenString).Value)
	}

	// Convert field name from CLI format (dashes) to config format (snake_case)
	field = cliFieldToConfigField(field)

	// Create operation
	operation := Operation{
		Type:  OpSet,
		Path:  configPath + "." + name + "." + field,
		Value: strings.Join(values, " "),
	}

	return []Operation{operation}, nil
}

// parseUnset handles unset command
func (p *Parser) parseUnset(configPath, name string) ([]Operation, error) {
	p.consume(TokenUnset)

	// Get the field name
	token := p.peek()
	if token.Type != TokenString {
		return nil, fmt.Errorf("expected field name after unset, got %s", token.Value)
	}
	field := p.consume(TokenString).Value

	// Convert field name from CLI format (dashes) to config format (snake_case)
	field = cliFieldToConfigField(field)

	// Create operation
	operation := Operation{
		Type: OpUnset,
		Path: configPath + "." + name + "." + field,
	}

	return []Operation{operation}, nil
}

// parseAppend handles append command
func (p *Parser) parseAppend(configPath, name string) ([]Operation, error) {
	p.consume(TokenAppend)

	// Get the field name
	token := p.peek()
	if token.Type != TokenString {
		return nil, fmt.Errorf("expected field name after append, got %s", token.Value)
	}
	field := p.consume(TokenString).Value

	// Get the values
	var values []string
	for p.pos < len(p.tokens) {
		token := p.peek()
		if token.Type == TokenNext || token.Type == TokenEnd || token.Type == TokenConfig || token.Type == TokenShow || token.Type == TokenEdit || token.Type == TokenDelete {
			break
		}
		values = append(values, p.consume(TokenString).Value)
	}

	// Convert field name from CLI format (dashes) to config format (snake_case)
	field = cliFieldToConfigField(field)

	// Create operation
	operation := Operation{
		Type:  OpAppend,
		Path:  configPath + "." + name + "." + field,
		Value: strings.Join(values, " "),
	}

	return []Operation{operation}, nil
}

// parseDelete handles delete command
func (p *Parser) parseDelete(configPath string) ([]Operation, error) {
	p.consume(TokenDelete)

	// Get the name or ID
	token := p.peek()
	if token.Type != TokenString {
		return nil, fmt.Errorf("expected name or ID after delete, got %s", token.Value)
	}
	name := p.consume(TokenString).Value

	// Create operation
	operation := Operation{
		Type: OpDelete,
		Path: configPath + "." + name,
	}

	return []Operation{operation}, nil
}

// parseShow handles show command
func (p *Parser) parseShow() ([]Operation, error) {
	p.consume(TokenShow)

	// Get optional section path (may be multiple words like "system interface")
	var sectionTokens []Token
	for p.pos < len(p.tokens) {
		token := p.peek()
		if token.Type != TokenString {
			break
		}
		sectionTokens = append(sectionTokens, token)
		p.consume(TokenString)
	}

	// Create operation
	var section string
	if len(sectionTokens) > 0 {
		section = p.tokensToString(sectionTokens)
	}
	operation := Operation{
		Type:    OpShow,
		Section: section,
	}

	return []Operation{operation}, nil
}

// consume consumes a token of the specified type and returns it
func (p *Parser) consume(tokenType TokenType) Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: TokenEOF}
	}
	token := p.tokens[p.pos]
	p.pos++
	return token
}

// peek returns the next token without consuming it
func (p *Parser) peek() Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: TokenEOF}
	}
	return p.tokens[p.pos]
}

// tokensToString converts a slice of tokens to a string
func (p *Parser) tokensToString(tokens []Token) string {
	var parts []string
	for _, token := range tokens {
		parts = append(parts, token.Value)
	}
	return strings.Join(parts, " ")
}

// mapSectionToPath maps CLI section paths to config paths
func mapSectionToPath(section string) (string, error) {
	// Normalize the section by converting to lowercase and trimming whitespace
	section = strings.TrimSpace(strings.ToLower(section))

	// Map CLI section paths to config paths
	switch section {
	case "firewall policy":
		return "policies", nil
	case "system interface":
		return "interfaces", nil
	case "firewall address":
		return "addresses", nil
	case "system dhcp server":
		return "dhcp", nil
	case "firewall service custom":
		return "services", nil
	case "firewall addrgrp":
		return "address_groups", nil
	case "firewall vip":
		return "nat.port_forwards", nil
	default:
		return "", fmt.Errorf("unknown section: %s", section)
	}
}

// cliFieldToConfigField converts field names from CLI format (dashes) to config format (snake_case)
func cliFieldToConfigField(field string) string {
	// Convert dashes to underscores (CLI format to config format)
	return strings.ReplaceAll(field, "-", "_")
}
