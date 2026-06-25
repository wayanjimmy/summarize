# YouTube failure path — non-existent video ID
# Verifies that a valid YouTube URL with an invalid video ID
# eventually results in a failed run with an error code.

# Step 1: Submit a YouTube URL with non-existent video ID
POST {{HOST}}/v1/summaries
Content-Type: application/json
{
  "url": "https://www.youtube.com/watch?v=00000000000"
}
HTTP 202
[Captures]
run_id: jsonpath "$.run_id"
[Asserts]
jsonpath "$.status" == "queued"

# Step 2: Poll until failed (yt-dlp will fail to find this video)
# The global --retry flag retries this request until the assertion passes.
GET {{HOST}}/v1/runs/{{run_id}}/status
HTTP 200
[Asserts]
jsonpath "$.run_id" == "{{run_id}}"
jsonpath "$.status" == "failed"
jsonpath "$.error.code" exists
jsonpath "$.error.message" exists

# Step 3: Verify the summary endpoint shows the same failure
GET {{HOST}}/v1/summaries/{{run_id}}
HTTP 200
[Asserts]
jsonpath "$.run_id" == "{{run_id}}"
jsonpath "$.status" == "failed"
jsonpath "$.error.code" exists
jsonpath "$.input_type" == "youtube"
jsonpath "$.source_url" == "https://www.youtube.com/watch?v=00000000000"
