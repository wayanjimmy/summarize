# YouTube summary lifecycle (standard watch URL)
# Uses a real video: https://www.youtube.com/watch?v=l4_TROZ3oWk
# Retry happens via --retry CLI flag on the polling step.

# Step 1: Submit a YouTube URL (standard watch format)
POST {{HOST}}/v1/summaries
Content-Type: application/json
{
  "url": "https://www.youtube.com/watch?v=l4_TROZ3oWk"
}
HTTP 202
[Captures]
run_id: jsonpath "$.run_id"
[Asserts]
jsonpath "$.status" == "queued"
jsonpath "$.status_url" == "/v1/runs/{{run_id}}/status"
jsonpath "$.result_url" == "/v1/summaries/{{run_id}}"

# Step 2: Poll status until terminal (retried by global --retry flag)
GET {{HOST}}/v1/runs/{{run_id}}/status
HTTP 200
[Asserts]
jsonpath "$.run_id" == "{{run_id}}"
jsonpath "$.status" matches "^(succeeded|failed)$"

# Step 3: Retrieve summary — verify YouTube metadata
GET {{HOST}}/v1/summaries/{{run_id}}
HTTP 200
[Asserts]
jsonpath "$.run_id" == "{{run_id}}"
jsonpath "$.input_type" == "youtube"
jsonpath "$.source_url" == "https://www.youtube.com/watch?v=l4_TROZ3oWk"
jsonpath "$.youtube.video_id" == "l4_TROZ3oWk"
jsonpath "$.youtube.title" exists
jsonpath "$.engine" exists
jsonpath "$.status" matches "^(succeeded|failed)$"
