# Store package

Add persistence interfaces here. Keep persistence types separate from API DTOs.

Suggested split:

- `internal/api`: generated transport DTOs and route adapters.
- `internal/domain`: game/turn/order domain concepts.
- `internal/store`: database access and transaction boundaries.
- `internal/handlers`: generated handler interface implementation.
