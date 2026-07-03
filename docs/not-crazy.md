# Crazy?

I'm using OpenAPI to write the spec for a server (written in Go). I'm using one of their tools to generate the Go structure. It's an easy effort.

The Go code it generates feels crufty.

I'm thinking of asking the coding agent to read the OpenAPI spec and generate the DTO and an interface for the routes.

I'd then have another coding agent write the Go handlers to implement the interfaces.

Is that crazy? Would the code feel cleaner?

## Response

Not crazy. It is actually a pretty sane direction — **as long as the OpenAPI spec remains the contract and the generated/agent-written code is treated as disposable scaffolding, not sacred application architecture**.

The smell you are reacting to is common. OpenAPI generators often optimize for complete spec coverage, repeatability, and edge cases, not for code that feels like a Go engineer lovingly designed it. Even `oapi-codegen` says it aims for idiomatic Go “where possible” but also chooses simple generated code and sometimes duplicate code over prettier refactoring. ([GitHub][1]) OpenAPI Generator’s Go server generator also has options like `onlyInterfaces`, which is basically an acknowledgement that many teams want contracts/interfaces without all the generated service cruft. ([OpenAPI Generator][2])

The architecture I’d aim for is:

```text
OpenAPI spec
   ↓
Generated or agent-produced transport package
   - request/response DTOs
   - route/server interface
   - parameter structs
   - maybe response wrapper types
   ↓
Handwritten handlers/adapters
   - validate/normalize where needed
   - map DTOs ↔ domain types
   - call application services
   ↓
Domain/application layer
   - clean Go interfaces
   - no OpenAPI imports
```

So yes: asking an agent to read the OpenAPI spec and produce **DTOs plus a route interface** can absolutely lead to cleaner code. But I would be careful about replacing deterministic codegen with “LLM codegen” unless you put guardrails around it.

The risk is not that the agent writes ugly code. The risk is that it writes **clean-looking but subtly wrong** code: missing `nullable`, mishandling required-but-zero-value fields, flattening `oneOf` incorrectly, losing enum constraints, treating query arrays wrong, forgetting non-JSON content types, or simplifying response status variants. Traditional generators are annoying, but they are annoying in repeatable ways.

A good compromise: use a real generator for the hard protocol edge cases, then keep it isolated. For example, `ogen` already generates a `Handler` interface from OpenAPI and asks you to implement that interface; its docs show generated files including interfaces, handlers, schemas, validators, router, encoders, and decoders. ([Ogen][3]) That may give you the separation you want without inventing your own codegen pipeline.

My recommendation:

**Do not let the agent invent the whole OpenAPI-to-Go mapping from scratch unless the API is small and stable.** Instead, either:

1. Use `ogen` or `oapi-codegen` and configure it to generate only the pieces you want.
2. Put generated code in something like `internal/api/gen` or `pkg/httpapi`.
3. Never edit generated files.
4. Write clean handlers in your own package.
5. Keep domain/service interfaces independent of OpenAPI DTOs.
6. Let the coding agent write the handler implementations and mapping code.

Where the coding agent is most useful is not the DTO generation itself. It is great at writing the boring glue:

```go
func (h *Handlers) CreateWidget(ctx context.Context, req api.CreateWidgetRequest) (api.CreateWidgetResponse, error) {
    cmd := widgets.CreateCommand{
        Name: req.Name,
        Size: domain.WidgetSize(req.Size),
    }

    widget, err := h.widgets.Create(ctx, cmd)
    if err != nil {
        return mapCreateWidgetError(err), nil
    }

    return api.CreateWidget201JSONResponse{
        ID: widget.ID.String(),
        Name: widget.Name,
    }, nil
}
```

That code can feel clean because it is yours. The generated code can remain a sealed-off protocol adapter.

The version I would **not** do is:

```text
Agent reads OpenAPI → invents DTOs/interfaces
Another agent implements handlers
No deterministic regeneration
No spec conformance tests
```

That will feel clean for a week, then drift.

The version I **would** do is:

```text
OpenAPI spec
↓
Pinned generator or tightly constrained agent script
↓
Generated DTOs/interfaces
↓
Compile-time interface implementation checks
↓
Handler agent writes implementation
↓
Contract tests / golden response tests / OpenAPI validator
```

So the answer is: **not crazy; likely cleaner; but make the transport boundary explicit and keep generation deterministic.** The cleanest Go code usually comes from letting OpenAPI own the HTTP edge, while your actual application code remains normal Go.

[1]: https://github.com/oapi-codegen/oapi-codegen "GitHub - oapi-codegen/oapi-codegen: Generate Go client and server boilerplate from OpenAPI 3 specifications · GitHub"
[2]: https://openapi-generator.tech/docs/generators/go-server/ "Documentation for the go-server Generator | OpenAPI Generator"
[3]: https://ogen.dev/docs/intro/ "Getting started | ogen"

