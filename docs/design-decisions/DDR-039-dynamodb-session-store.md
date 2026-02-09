# DDR-039: DynamoDB SessionStore for Persistent Multi-Step State

**Date**: 2026-02-09  
**Status**: Implementing  
**Iteration**: Phase 2 Cloud Deployment

## Context

The Lambda handler stores all job state (triage, selection, enhancement, download, description) in Go maps protected by mutexes (`var jobs = make(map[string]*triageJob)`, etc.). This works for single-container request flows but is fundamentally unreliable for the multi-step selection workflow because:

1. **Lambda containers are ephemeral** — cold starts create fresh maps; warm containers are reused unpredictably by AWS.
2. **Concurrent containers don't share state** — under load, AWS may spawn multiple containers for the same function, each with its own in-memory maps.
3. **No persistence across deploys** — code updates terminate all running containers, losing all job state.
4. **Step Functions fan-out** — the planned multi-Lambda architecture (DDR-035) processes items in separate Lambda invocations that cannot share in-memory state.

The CDK infrastructure (StorageStack, DDR-035) already provisions a DynamoDB table `media-selection-sessions` with single-table design (PK/SK), PAY_PER_REQUEST billing, and 24-hour TTL auto-cleanup via `expiresAt`.

## Decision

Implement `internal/store` package with:

1. A **`SessionStore` interface** defining typed CRUD operations for each record type: session metadata, selection jobs, enhancement jobs, download jobs, description jobs, and post groups.
2. A **`DynamoStore` struct** implementing `SessionStore` using the AWS SDK v2 DynamoDB client.
3. **Full-item PutItem/GetItem** operations (no partial updates) for simplicity and correctness — the handler reads the full record, modifies it in memory, and writes it back.
4. **DynamoDB Query with SK prefix filtering** for session invalidation (delete downstream jobs when the user navigates back).
5. **Automatic TTL** (24 hours) on all records, matching the S3 media lifecycle policy.
6. A single `UpdateSessionStatus` method using DynamoDB `UpdateItem` for the common atomic status-update case.

### Single-Table Key Schema

| Record Type | PK | SK | Contents |
|---|---|---|---|
| Session metadata | `SESSION#{sessionId}` | `META` | status, tripContext, uploadedKeys |
| Selection job | `SESSION#{sessionId}` | `SELECTION#{jobId}` | status, selected[], excluded[], sceneGroups[] |
| Enhancement job | `SESSION#{sessionId}` | `ENHANCE#{jobId}` | status, items[], totalCount, completedCount |
| Download job | `SESSION#{sessionId}` | `DOWNLOAD#{jobId}` | status, bundles[] |
| Description job | `SESSION#{sessionId}` | `DESC#{jobId}` | status, caption, hashtags, history[] |
| Post group | `SESSION#{sessionId}` | `GROUP#{groupId}` | name, mediaKeys[], caption, publishStatus |

## Rationale

- **DynamoDB is already provisioned** via CDK (DDR-035). Zero new infrastructure work.
- **Single-table design** co-locates all session data under one partition key. `Query(PK = SESSION#abc)` retrieves the entire session state in one call for invalidation.
- **Full PutItem writes** avoid the complexity of partial updates (`UpdateExpression` with nested list/map operations). The trade-off is read-modify-write for incremental updates, which is acceptable because concurrent writes to the same job are rare (Step Functions serializes processing within a pipeline).
- **PAY_PER_REQUEST billing** means zero cost at rest and pennies per session (~10 reads + writes per session).
- **TTL auto-cleanup** eliminates the need for manual garbage collection. Records expire after 24 hours, matching S3 media expiration.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Keep in-memory maps with sticky routing | API Gateway doesn't support sticky sessions; Lambda container reuse is non-deterministic |
| ElastiCache (Redis) | Over-provisioned for this workload; adds VPC complexity and ~$15/month minimum |
| S3 JSON files for state | High latency for frequent polling (selection status checks); no atomic updates |
| DynamoDB with fine-grained UpdateExpressions | Significantly more complex code for nested list/map updates; marginal benefit given low concurrency |

## Consequences

**Positive:**
- Job state survives Lambda cold starts, container recycling, and redeployments
- Multiple Lambda functions (API, Thumbnail, Selection, Enhancement) can read/write shared state
- Session invalidation becomes a simple Query + BatchWriteItem instead of iterating in-memory maps
- TTL auto-cleanup replaces manual cleanup logic
- Cost: ~$0.001 per session (a few DynamoDB reads + writes)

**Trade-offs:**
- Read-modify-write pattern for incremental updates (e.g., marking one enhancement item complete) — acceptable because Step Functions serializes per-item processing
- DynamoDB 400KB item size limit — a selection job with 50 items and full metadata is ~50-100KB, well within limits
- Requires AWS SDK v2 DynamoDB dependency (`github.com/aws/aws-sdk-go-v2/service/dynamodb`)
- Handler migration: existing in-memory job code must be refactored to use the SessionStore interface (incremental migration path available)

## Related Documents

- [DDR-035: Multi-Lambda Deployment Architecture](./DDR-035-multi-lambda-deployment.md) — provisions the DynamoDB table via CDK
- [DDR-028: Security Hardening](./DDR-028-security-hardening.md) — session ownership enforcement
- [DDR-037: Step Navigation and State Invalidation](./DDR-037-step-navigation-and-state-invalidation.md) — downstream invalidation cascade
