# TASK 346: Publish MCP tool refreshes atomically

## Why

MCP refresh publishes internal tool state before rebuilding the SDK-visible tool catalog. A conversion or registration failure can therefore leave internal and SDK catalogs at different generations, with a partially replaced upstream registry.

## Acceptance

- A refresh prepares the complete internal and SDK tool generations before publishing either one.
- Any conversion, removal, or registration failure leaves the previously published generation fully usable.
- Generation and decision-cache changes become visible atomically with the matching SDK catalog.
- Fault-injection tests cover failures at every catalog replacement stage.

## Sub-Tasks

- [x] Define a staged MCP refresh transaction for internal and SDK state.
- [x] Implement rollback-safe SDK catalog replacement.
- [x] Add partial-failure and concurrent-observer regression tests.
- [x] Run focused MCP gateway tests and race tests.

## Notes

- Verified from OMP session `01a04cc5-6a01-7526-95bb-b2715553daf3`, finding #91.
- Current-code verification confirmed the finding. The gateway previously published internal state before `publishUpstreamTools`, while SDK replacement mutated one name at a time and could return after partial removal or registration.
- `github.com/modelcontextprotocol/go-sdk` v1.7.0 is the latest stable release currently advertised by the Go module proxy. The official SDK `main` commit `21c18c6229e1c6d1d53d9a57475a2f65cc508cf3` still exposes only `AddTool` and `RemoveTools`; neither the release nor `main` exposes an atomic replace operation.
- The gateway now prepares validated contracts and SDK definitions before notification, then publishes one immutable generation containing the contracts, sorted advertised definitions, generation identity, and decision cache. The SDK server retains only a private valid sentinel used to trigger list-change notifications; receiving middleware serves `tools/list` and `tools/call` from that same generation, so SDK `AddTool` and `RemoveTools` cannot expose an intermediate catalog.
- Conversion failure, notification failure, and a failed direct SDK conversion replacement are covered by regression tests that prove the previous generation or SDK catalog remains usable. A concurrent observer repeatedly reads the generation under the publication lock and cannot observe split state. Bounded opaque cursors cover the published pagination path.
- Focused verification passed with `go test ./internal/mcpgateway -run 'TestGatewayPublishesToolGenerationAsOneSnapshot|TestGatewayStagesSDKConversionBeforePublication|TestGatewayRefreshNotificationFailureKeepsPublishedGeneration|TestGatewayReplacementConversionFailureKeepsSDKCatalog|TestGatewayListsPublishedCatalogWithBoundedOpaqueCursor|TestValidatedToolContractMatchesAdvertisedSDKTool|TestGatewayRefreshesChangedToolContractEndToEnd'` and `go test -race ./internal/mcpgateway -run 'TestGatewayPublishesToolGenerationAsOneSnapshot|TestGatewayRefreshNotificationFailureKeepsPublishedGeneration|TestGatewayReplacementConversionFailureKeepsSDKCatalog|TestGatewayRefreshesChangedToolContractEndToEnd'`.

## Deviations

None.
