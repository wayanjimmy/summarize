# YouTube summary lifecycle using the agy engine
# Same video as create_youtube.hurl but with engine=agy explicitly.
# Video: https://www.youtube.com/watch?v=l4_TROZ3oWk

# Step 1: Submit a YouTube URL with agy engine
POST {{HOST}}/v1/summaries
Content-Type: application/json
{
  "url": "https://www.youtube.com/watch?v=l4_TROZ3oWk",
  "engine": "agy"
}
HTTP 202
[Captures]
run_id: jsonpath "$.run_id"
[Asserts]
jsonpath "$.status" == "queued"
jsonpath "$.status_url" == "/v1/runs/{{run_id}}/status"
jsonpath "$.result_url" == "/v1/summaries/{{run_id}}"

# Step 2: Poll status until terminal
GET {{HOST}}/v1/runs/{{run_id}}/status
HTTP 200
[Asserts]
jsonpath "$.run_id" == "{{run_id}}"
jsonpath "$.status" matches "^(succeeded|failed)$"

# Step 3: Retrieve summary — verify agy engine was used
GET {{HOST}}/v1/summaries/{{run_id}}
HTTP 200
[Asserts]
jsonpath "$.run_id" == "{{run_id}}"
jsonpath "$.input_type" == "youtube"
jsonpath "$.source_url" == "https://www.youtube.com/watch?v=l4_TROZ3oWk"
jsonpath "$.youtube.video_id" == "l4_TROZ3oWk"
jsonpath "$.youtube.title" exists
jsonpath "$.engine" == "agy"
jsonpath "$.status" matches "^(succeeded|failed)$"
