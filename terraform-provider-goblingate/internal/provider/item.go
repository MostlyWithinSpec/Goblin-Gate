package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"

	"github.com/networkgoblinlabs/goblin-gate/terraform-provider-goblingate/internal/client"
)

// itemSpec describes one kind of config object (an address, a policy, a
// DHCP static lease…): where it lives in the config document and how its
// JSON maps to the Terraform model M. itemResource does the rest.
type itemSpec[M any] struct {
	typeName string // after "goblingate_"
	noun     string // in commit comments: "address"
	schema   schema.Schema

	// id is the Terraform ID; label names the object in commit comments.
	id    func(m *M) string
	label func(m *M) string
	setID func(m *M, id string)
	// fromID fills the identity fields from an imported ID.
	fromID func(id string, m *M) error

	// lookup finds the object in a config document: its JSON value and its
	// config path (for PATCH).
	lookup func(cfg client.Config, m *M) (value any, path string, ok bool)
	// newPath is the path of an object that does not exist yet.
	newPath func(m *M) string
	// value is the JSON to write: existing (nil when creating) with the
	// managed fields replaced, so fields Terraform does not manage survive.
	value func(m *M, existing any) any
	// read fills the model from the object's JSON; prior is the state
	// before, for keeping "" and [] apart from unset.
	read func(raw any, m *M)
	// prepare runs before a create, on the candidate (e.g. to pick a free
	// policy ID). It may return an error to refuse.
	prepare func(cand client.Config, m *M) error
}

type itemResource[M any] struct {
	spec *itemSpec[M]
	c    *client.Client
}

var _ resource.ResourceWithImportState = (*itemResource[struct{}])(nil)

func (r *itemResource[M]) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.spec.typeName
}

func (r *itemResource[M]) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = r.spec.schema
}

func (r *itemResource[M]) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("got %T", req.ProviderData))
		return
	}
	r.c = c
}

func (r *itemResource[M]) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s := r.spec
	err := r.c.Change(ctx, "create "+s.noun+" "+s.label(&m), func(e client.Edit) error {
		cand, err := e.Candidate()
		if err != nil {
			return err
		}
		if s.prepare != nil {
			if err := s.prepare(cand, &m); err != nil {
				return err
			}
		}
		if _, _, exists := s.lookup(cand, &m); exists {
			return fmt.Errorf("%s %s already exists on the gate; import it (terraform import) instead of creating it", s.noun, s.label(&m))
		}
		return e.Set(s.newPath(&m), s.value(&m, nil))
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create the "+s.noun, err.Error())
		return
	}
	s.setID(&m, s.id(&m))
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *itemResource[M]) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m M
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cfg, err := r.c.Running(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Could not read the running config", err.Error())
		return
	}
	raw, _, ok := r.spec.lookup(cfg, &m)
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	r.spec.read(raw, &m)
	r.spec.setID(&m, r.spec.id(&m))
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *itemResource[M]) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s := r.spec
	err := r.c.Change(ctx, "update "+s.noun+" "+s.label(&m), func(e client.Edit) error {
		cand, err := e.Candidate()
		if err != nil {
			return err
		}
		existing, p, ok := s.lookup(cand, &m)
		if !ok {
			return fmt.Errorf("%s %s is gone from the gate (removed outside Terraform); run terraform apply again to create it", s.noun, s.label(&m))
		}
		return e.Set(p, s.value(&m, existing))
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not update the "+s.noun, err.Error())
		return
	}
	s.setID(&m, s.id(&m))
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *itemResource[M]) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m M
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s := r.spec
	err := r.c.Change(ctx, "delete "+s.noun+" "+s.label(&m), func(e client.Edit) error {
		cand, err := e.Candidate()
		if err != nil {
			return err
		}
		_, p, ok := s.lookup(cand, &m)
		if !ok {
			return nil // already gone
		}
		return e.Delete(p)
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not delete the "+s.noun, err.Error())
	}
}

func (r *itemResource[M]) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var m M
	if err := r.spec.fromID(req.ID, &m); err != nil {
		resp.Diagnostics.AddError("Bad import ID", err.Error())
		return
	}
	cfg, err := r.c.Running(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Could not read the running config", err.Error())
		return
	}
	raw, _, ok := r.spec.lookup(cfg, &m)
	if !ok {
		resp.Diagnostics.AddError("Not found", fmt.Sprintf("there is no %s %q in the running config", r.spec.noun, req.ID))
		return
	}
	r.spec.read(raw, &m)
	r.spec.setID(&m, r.spec.id(&m))
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
