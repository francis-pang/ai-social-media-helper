# DDR-040: Instagram Publishing Client

**Date**: 2026-02-09  
**Status**: Draft  
**Iteration**: Phase 2 Cloud Deployment — Publishing Step

## Context

The media selection workflow currently ends at Step 8 (AI caption generation). The user downloads ZIP bundles and manually uploads to Instagram. The entire pipeline — triage, selection, enhancement, grouping, captioning — exists to produce Instagram-ready content, but the final mile (publishing) is manual.

The `PostGroup` data model already includes `PublishStatus` and `InstagramPostID` fields (see `internal/store/store.go`), and the CDK infrastructure provisions SSM Parameter Store paths for Instagram credentials (`/ai-social-media/prod/instagram-access-token`, `/ai-social-media/prod/instagram-user-id`). The architecture is ready for publishing — only the Instagram API client and the publish step UI are missing.

### Current Workflow (Steps 1-8)

```
Upload → Triage → Selection → Enhancement → Grouping → Download → Captioning → ???
```

### Target Workflow (Steps 1-9)

```
Upload → Triage → Selection → Enhancement → Grouping → Download → Captioning → Publish
```

## Decision

Implement an `internal/instagram/` package providing an Instagram Graph API client for publishing carousel and single-media posts. Add a Step 9 "Publish" stage to the frontend and a set of `/api/publish/` endpoints to the API Lambda.

### 1. Instagram Graph API Integration

Instagram publishing uses the [Instagram Graph API](https://developers.facebook.com/docs/instagram-platform/instagram-api-with-instagram-login/content-publishing) (also known as the Instagram API with Instagram Login), which requires:

1. A **Facebook App** in the Meta Developer Console
2. An **Instagram Business or Creator Account** linked to a Facebook Page
3. **OAuth 2.0 access tokens** with specific permissions
4. Media items must be **publicly accessible via URL** (Instagram fetches them from the URL)

#### Publishing Flow (Carousel)

Instagram carousel publishing is a 3-step process:

```
Step A: Create media containers (one per item)
  POST /me/media
    image_url={presigned_s3_url}&is_carousel_item=true
    — OR —
    video_url={presigned_s3_url}&is_carousel_item=true&media_type=VIDEO

Step B: Create carousel container
  POST /me/media
    media_type=CAROUSEL&children={id1,id2,...}&caption={text}

Step C: Publish the carousel
  POST /me/media_publish
    creation_id={carousel_container_id}
```

For single-media posts (1 item in a group), Steps A and B collapse into a single container creation with the caption.

#### Publishing Flow (Single Image)

```
Step A: Create media container
  POST /me/media
    image_url={presigned_s3_url}&caption={text}

Step B: Publish
  POST /me/media_publish
    creation_id={container_id}
```

#### Publishing Flow (Single Video / Reel)

```
Step A: Create media container
  POST /me/media
    video_url={presigned_s3_url}&media_type=REELS&caption={text}

Step B: Poll container status (video processing)
  GET /{container_id}?fields=status_code
  → Repeat until status_code = FINISHED (or ERROR)

Step C: Publish
  POST /me/media_publish
    creation_id={container_id}
```

### 2. Package Design: `internal/instagram/`

```go
// internal/instagram/client.go

// Client provides methods for publishing to Instagram via the Graph API.
type Client struct {
    httpClient  *http.Client
    accessToken string
    userID      string
    baseURL     string // https://graph.instagram.com/v22.0
}

// NewClient creates an Instagram API client.
// accessToken and userID are loaded from SSM Parameter Store at Lambda cold start.
func NewClient(accessToken, userID string) *Client

// --- Container creation ---

// CreateImageContainer creates an image media container for carousel use.
// imageURL must be a publicly accessible URL (presigned S3 GET URL).
func (c *Client) CreateImageContainer(ctx context.Context, imageURL string, isCarousel bool) (containerID string, err error)

// CreateVideoContainer creates a video/reel media container.
// videoURL must be a publicly accessible URL.
// For carousel items, set isCarousel=true. For standalone reels, set isCarousel=false.
func (c *Client) CreateVideoContainer(ctx context.Context, videoURL string, isCarousel bool) (containerID string, err error)

// CreateCarouselContainer creates a carousel container from child container IDs.
// caption includes the full caption text. hashtags are appended.
// locationID is an optional Instagram location ID (from location search).
func (c *Client) CreateCarouselContainer(ctx context.Context, children []string, caption string) (containerID string, err error)

// CreateSinglePostContainer creates a single-media post container with caption.
func (c *Client) CreateSinglePostContainer(ctx context.Context, mediaURL string, mediaType string, caption string) (containerID string, err error)

// --- Publishing ---

// Publish publishes a media container (carousel or single).
// Returns the Instagram media ID of the published post.
func (c *Client) Publish(ctx context.Context, containerID string) (mediaID string, err error)

// --- Status polling ---

// ContainerStatus returns the processing status of a media container.
// Used for video containers which require server-side processing.
// Returns: "IN_PROGRESS", "FINISHED", or "ERROR".
func (c *Client) ContainerStatus(ctx context.Context, containerID string) (status string, err error)

// WaitForContainer polls container status until FINISHED or ERROR.
// Uses exponential backoff: 5s, 10s, 20s, 30s (max), with a total timeout.
func (c *Client) WaitForContainer(ctx context.Context, containerID string, timeout time.Duration) error

// --- Utility ---

// SearchLocation searches for an Instagram location by name and coordinates.
// Returns a location ID suitable for tagging posts.
func (c *Client) SearchLocation(ctx context.Context, query string, lat, lng float64) (locationID string, err error)
```

### 3. API Endpoints

New endpoints added to the API Lambda (`cmd/media-lambda/`):

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/api/publish/start` | Start publishing a post group to Instagram |
| `GET` | `/api/publish/{id}/status` | Poll publishing progress |

#### `POST /api/publish/start`

Request body:

```json
{
  "sessionId": "abc-123",
  "groupId": "group-1"
}
```

The handler:

1. Reads the `PostGroup` from DynamoDB (gets `MediaKeys`, `Caption`)
2. Reads the `DescriptionJob` for the group (gets `Hashtags`, `LocationTag`)
3. For each media item in the group:
   a. Generates a presigned S3 GET URL (1-hour expiry, public access)
   b. Calls `CreateImageContainer` or `CreateVideoContainer` (carousel item)
4. If the group has video items, waits for all video containers to finish processing
5. Creates a carousel container (or single post) with the full caption + hashtags
6. Publishes the container
7. Updates `PostGroup.PublishStatus` to `"published"` and stores the `InstagramPostID` in DynamoDB

#### `GET /api/publish/{id}/status`

Returns the current publishing status:

```json
{
  "status": "publishing",
  "phase": "creating_containers",
  "progress": { "completed": 3, "total": 8 },
  "instagramPostId": null
}
```

Phases: `"creating_containers"` → `"processing_videos"` → `"creating_carousel"` → `"publishing"` → `"published"`

### 4. Credential Management

Instagram credentials are loaded from SSM Parameter Store at Lambda cold start, following the same pattern as the Gemini API key (see `cmd/media-lambda/main.go` `init()`):

| SSM Parameter | Purpose |
|---|---|
| `/ai-social-media/prod/instagram-access-token` | Long-lived Instagram access token |
| `/ai-social-media/prod/instagram-user-id` | Instagram Business/Creator account user ID |

These are already referenced in the CDK infrastructure (DDR-035).

### 5. Media URL Strategy

Instagram's Graph API requires media to be accessible via a **public URL**. The API fetches the media from the URL server-side — you cannot upload binary data directly.

**Strategy: Presigned S3 GET URLs**

The enhanced media files are already stored in S3 at `{sessionId}/enhanced/{filename}`. We generate presigned GET URLs with a 1-hour expiry for each media item when creating containers. These URLs are publicly accessible (no authentication) and expire after Instagram has fetched the content.

This approach:
- Requires no changes to S3 bucket policy (presigned URLs work with private buckets)
- Reuses existing presigned URL infrastructure (`s3.PresignClient`)
- URLs expire after 1 hour — sufficient for Instagram to fetch and process
- No new public endpoints or CloudFront configurations needed

### 6. Caption Assembly

The full caption posted to Instagram is assembled from the `DescriptionJob` data:

```
{caption text from AI}

{hashtags as space-separated #tags}
```

Example:

```
Found the most incredible little ramen spot hidden down this tiny alley in Shibuya 🍜 
The owner has been making the same recipe for 40 years and you can taste every year of 
dedication in that broth. Sometimes the best meals aren't the ones you plan — they're the 
ones you stumble into at 11pm after getting completely lost 🗺️✨

Who else lives for those unexpected food finds while traveling? Drop your best 
accidental restaurant discovery below 👇

#Tokyo #Shibuya #Ramen #JapanFood #TokyoFoodie #StreetFood #RamenLovers 
#JapanTravel #FoodieFinds #HiddenGems #LateNightEats #TravelEats 
#JapaneseFood #AsiaTravel #FoodTravel #NoodleLover #Wanderlust
```

### 7. Frontend: Publish Step (Step 9)

A new `PublishView.tsx` component showing each post group with:

- Thumbnail mosaic preview of the group's media
- The finalized caption (read-only preview with hashtags)
- A "Publish to Instagram" button per group
- Status indicator: `ready` → `publishing` → `published` (with Instagram post ID link)
- Error state with retry option
- An "Open on Instagram" link after successful publish

```
┌────────────────────────────────────────────────────────────┐
│  PUBLISH TO INSTAGRAM                                       │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Post Group: "Tokyo Day 1 — Temples"    [5 items]     │   │
│  │ ┌─────┐ ┌─────┐ ┌─────┐ ┌─────┐ ┌─────┐            │   │
│  │ │img01│ │img02│ │img03│ │img04│ │vid01│            │   │
│  │ └─────┘ └─────┘ └─────┘ └─────┘ └─────┘            │   │
│  │                                                        │   │
│  │ Caption preview:                                       │   │
│  │ "Found the most incredible little ramen spot..."       │   │
│  │ #Tokyo #Shibuya #Ramen ...                             │   │
│  │                                                        │   │
│  │              [✓ Published — View on Instagram]          │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Post Group: "Tokyo Day 2 — Street Food"  [3 items]   │   │
│  │ ┌─────┐ ┌─────┐ ┌─────┐                              │   │
│  │ │img05│ │img06│ │vid02│                              │   │
│  │ └─────┘ └─────┘ └─────┘                              │   │
│  │                                                        │   │
│  │ Caption: "Street food is not a snack — it's a..."     │   │
│  │                                                        │   │
│  │              [Publish to Instagram →]                   │   │
│  └──────────────────────────────────────────────────────┘   │
└────────────────────────────────────────────────────────────┘
```

### 8. Rate Limits and Error Handling

#### Instagram API Rate Limits

| Limit | Value | Handling |
|-------|-------|----------|
| Content Publishing | 50 posts per 24 hours | Check before publishing; warn user if approaching limit |
| API calls | 200 calls per user per hour | Unlikely to hit with normal use (each carousel = ~25 calls max) |
| Carousel items | 20 max per carousel | Already enforced by PostGrouper UI (DDR-033) |
| Video size | 1 GB max | Well within limits (enhanced videos are compressed) |
| Image size | 8 MB max | Enhanced photos are typically 2-5 MB |

#### Error Handling Strategy

| Error | Recovery |
|-------|----------|
| Invalid/expired access token | Return clear error; user must re-authenticate and update SSM parameter |
| Video processing failed | Retry container creation up to 2 times with backoff |
| Container creation failed (bad URL) | Regenerate presigned URL and retry once |
| Rate limit exceeded | Return 429 with retry-after; frontend shows "try again later" |
| Network timeout | Retry with exponential backoff (max 3 attempts) |
| Partial carousel failure (some items published) | Roll back by not publishing; inform user which items failed |

### 9. DynamoDB Schema Addition

New record type for publish jobs:

| Record Type | PK | SK | Contents |
|---|---|---|---|
| Publish job | `SESSION#{sessionId}` | `PUBLISH#{jobId}` | status, groupId, phase, progress, instagramPostId, error |

The `StepOrder` in `internal/store/store.go` expands to include `"publish"` after `"description"`.

## Rationale

### Why not use the Instagram Basic Display API?

The Basic Display API only supports reading user profile and media — it cannot publish content. The Instagram Graph API (via Instagram Login) is the only API that supports content publishing.

### Why presigned S3 URLs instead of a public CDN?

Presigned URLs are temporary (1-hour expiry) and require no infrastructure changes. A public CDN endpoint would expose enhanced media permanently and require bucket policy changes. Since Instagram fetches the content immediately during container creation, short-lived URLs are sufficient and more secure.

### Why publish from the API Lambda instead of a dedicated Publishing Lambda?

Publishing a carousel involves sequential API calls (create containers → create carousel → publish) with video processing waits. The total time for a 20-item carousel with videos could be 2-5 minutes. However:

- The API Lambda has a 30-second timeout, which is sufficient for the initial container creation calls
- Video container processing is polled asynchronously by the frontend
- The actual publish call is fast (< 5 seconds)
- If total processing exceeds 30 seconds, a Step Functions workflow can be added later

For v1, the publish operation runs as an asynchronous job: the API Lambda starts the publish, stores progress in DynamoDB, and a subsequent API call (or polling) retrieves the result. If publishing takes too long for a single Lambda invocation, the handler can be split into phases across multiple invocations (container creation phase → poll video processing → publish phase), with DynamoDB tracking progress between invocations.

### Why not use the Location Search API?

The Instagram location search API (`/search?type=place`) requires the Pages Search permission which adds complexity. For v1, the AI-generated `locationTag` text is displayed in the caption editor for the user to manually set in Instagram (location tags cannot be set via the Content Publishing API for personal accounts). This can be revisited if the Instagram API adds carousel location tagging support.

## Prerequisites: What You Need to Set Up

This section outlines the manual setup required before the Instagram client can work.

### 1. Facebook Developer Account and App

- [ ] Create a Facebook Developer account at [developers.facebook.com](https://developers.facebook.com)
- [ ] Create a new App (type: "Business" or "Consumer")
- [ ] Add the "Instagram" product to the app
- [ ] Note the **App ID** and **App Secret**

### 2. Instagram Business or Creator Account

- [ ] Convert your Instagram account to a **Business** or **Creator** account (Settings → Account → Switch to Professional Account)
- [ ] Link the Instagram account to a **Facebook Page** (even if the page is empty — this is required for API access)

### 3. Instagram Login and Permissions

The app needs these permissions via Instagram Login (OAuth 2.0):

| Permission | Purpose |
|---|---|
| `instagram_basic` | Read user profile and media |
| `instagram_content_publish` | Publish photos, videos, and carousels |

- [ ] Configure Instagram Login in the Facebook App dashboard
- [ ] Add the OAuth redirect URI
- [ ] Complete App Review for `instagram_content_publish` (required for production use)

> **Note**: During development, you can use the app in Development Mode with your own account (no App Review needed). App Review is only required when publishing for other users.

### 4. Generate a Long-Lived Access Token

Instagram access tokens have a lifecycle:

```
Short-lived token (1 hour)
  → Exchange for long-lived token (60 days)
    → Refresh before expiry (extends another 60 days)
```

- [ ] Generate a short-lived token via the Instagram Login OAuth flow
- [ ] Exchange for a long-lived token using the token exchange endpoint:
  ```
  GET https://graph.instagram.com/access_token
    ?grant_type=ig_exchange_token
    &client_secret={app_secret}
    &access_token={short_lived_token}
  ```
- [ ] Store the long-lived token in SSM Parameter Store:
  ```
  aws ssm put-parameter \
    --name /ai-social-media/prod/instagram-access-token \
    --type SecureString \
    --value "{long_lived_token}"
  ```

### 5. Get Instagram User ID

- [ ] Call the Instagram API to get your user ID:
  ```
  GET https://graph.instagram.com/v22.0/me?fields=id,username&access_token={token}
  ```
- [ ] Store in SSM Parameter Store:
  ```
  aws ssm put-parameter \
    --name /ai-social-media/prod/instagram-user-id \
    --type String \
    --value "{user_id}"
  ```

### 6. Token Refresh Strategy

Long-lived tokens expire after 60 days. Options for keeping the token alive:

| Strategy | Complexity | Reliability |
|---|---|---|
| **Manual refresh** — set a calendar reminder to refresh every 50 days | None | Low — easy to forget |
| **CloudWatch Events + Lambda** — scheduled Lambda refreshes the token every 50 days and updates SSM | Medium | High — fully automated |
| **Healthcheck refresh** — refresh the token on every Lambda cold start if it's > 30 days old | Low | Medium — depends on regular usage |

**Recommendation**: Start with manual refresh for v1. Add the CloudWatch scheduled Lambda in v2 if token expiry becomes a pain point. The refresh endpoint is:

```
GET https://graph.instagram.com/refresh_access_token
  ?grant_type=ig_refresh_token
  &access_token={long_lived_token}
```

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Instagram Private API (unofficial) | Against ToS; account ban risk; no stability guarantees |
| Buffer/Hootsuite/Later API | Adds a paid third-party dependency; publishing is simple enough to do directly |
| Manual publish forever | Defeats the purpose of an end-to-end automation tool |
| Upload binary to Instagram directly | Instagram Graph API does not support binary uploads; media must be URL-accessible |
| Facebook Graph API (publishing to Facebook page, auto-share to Instagram) | More complex; cross-posting doesn't support all Instagram features (reels, carousels with mixed media) |

## Consequences

**Positive:**
- Completes the end-to-end workflow — upload to published post with zero manual steps
- Presigned S3 URLs reuse existing infrastructure with no new public endpoints
- DynamoDB publish state integrates naturally with the existing session store
- The `PostGroup` data model already has the required fields (`PublishStatus`, `InstagramPostID`)
- Instagram carousel limit (20 items) is already enforced by the PostGrouper UI

**Trade-offs:**
- Requires Facebook Developer account and app setup (one-time manual process)
- Long-lived tokens expire after 60 days — needs a refresh strategy
- App Review required for `instagram_content_publish` in production mode (development mode works for personal use)
- Video processing on Instagram's side can take 30-120 seconds per video, increasing total publish time for video-heavy carousels
- Location tagging not available via API for carousel posts — user must add manually in Instagram
- The 50-post-per-24-hour limit is unlikely to be hit but should be surfaced in the UI

## Implementation Order

1. **`internal/instagram/client.go`** — Core API client with container creation, publishing, and status polling
2. **`internal/instagram/client_test.go`** — Unit tests with mocked HTTP responses
3. **SSM credential loading** — Add Instagram token + user ID loading in Lambda `init()`
4. **`cmd/media-lambda/publish.go`** — `/api/publish/start` and `/api/publish/{id}/status` handlers
5. **`internal/store/` additions** — `PublishJob` type, `PutPublishJob`, `GetPublishJob`
6. **`web/frontend/src/components/PublishView.tsx`** — Publish step UI
7. **`web/frontend/src/api/client.ts`** — `startPublish()`, `getPublishStatus()` API functions

## Related Documents

- [DDR-033: Post Grouping UI](./DDR-033-post-grouping-ui.md) — Post group data model and 20-item carousel limit
- [DDR-034: Download ZIP Bundling](./DDR-034-download-zip-bundling.md) — ZIP bundles for manual download fallback
- [DDR-036: AI Post Description](./DDR-036-ai-post-description.md) — Caption, hashtags, and location tag generation
- [DDR-039: DynamoDB SessionStore](./DDR-039-dynamodb-session-store.md) — Persistent session state and schema
- [DDR-035: Multi-Lambda Deployment](./DDR-035-multi-lambda-deployment.md) — SSM parameters and Lambda architecture
- [DDR-028: Security Hardening](./DDR-028-security-hardening.md) — Credential management and input validation
