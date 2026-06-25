# YouTube summary lifecycle (short youtu.be URL)
# Uses a real video: https://youtu.be/Sd34SoITwro
# Retry happens via --retry CLI flag on the polling step.

# Step 1: Submit a YouTube URL (short youtu.be format)
POST {{HOST}}/v1/summaries
Content-Type: application/json
{
  "url": "https://youtu.be/Sd34SoITwro"
}
HTTP 202
[Captures]
run_id: jsonpath "$.run_id"
[Asserts]
jsonpath "$.status" == "queued"

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
jsonpath "$.source_url" == "https://youtu.be/Sd34SoITwro"
jsonpath "$.youtube.video_id" == "Sd34SoITwro"
jsonpath "$.youtube.title" exists
jsonpath "$.status" matches "^(succeeded|failed)$"
