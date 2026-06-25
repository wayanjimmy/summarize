# Text summary lifecycle
# Submits a text snippet, polls until processed, retrieves the result.
# Retry happens via --retry CLI flag on the polling step.

# Step 1: Submit a text summary
GET {{HOST}}/v1/models
HTTP 200
[Asserts]
jsonpath "$.engines.pi.status" exists
jsonpath "$.engines.pi.available" exists
jsonpath "$.engines.pi.stale" exists

POST {{HOST}}/v1/summaries
Content-Type: application/json
{
  "text": "Go is a statically typed, compiled high-level programming language designed at Google. It is known for its simplicity, strong concurrency primitives (goroutines and channels), fast compilation, and excellent tooling. Go has become widely adopted for cloud services, CLI tools, and microservices.",
  "engine": "pi"
}
HTTP 202
[Captures]
run_id: jsonpath "$.run_id"
[Asserts]
jsonpath "$.run_id" matches "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
jsonpath "$.status" == "queued"
jsonpath "$.status_url" == "/v1/runs/{{run_id}}/status"
jsonpath "$.result_url" == "/v1/summaries/{{run_id}}"
header "Content-Type" == "application/json"

# Step 2: Poll status until terminal (retried by global --retry flag)
GET {{HOST}}/v1/runs/{{run_id}}/status
HTTP 200
[Asserts]
jsonpath "$.run_id" == "{{run_id}}"
jsonpath "$.status" matches "^(succeeded|failed)$"
jsonpath "$.stage" exists
jsonpath "$.created_at" matches "^[0-9]{4}-[0-9]{2}-[0-9]{2}T"
jsonpath "$.updated_at" matches "^[0-9]{4}-[0-9]{2}-[0-9]{2}T"
header "Content-Type" == "application/json"

# Step 3: Retrieve full summary
GET {{HOST}}/v1/summaries/{{run_id}}
HTTP 200
[Asserts]
jsonpath "$.run_id" == "{{run_id}}"
jsonpath "$.status" matches "^(succeeded|failed)$"
jsonpath "$.input_type" == "text"
jsonpath "$.engine" == "pi"
jsonpath "$.source_url" not exists
jsonpath "$.prompt" exists
jsonpath "$.created_at" matches "^[0-9]{4}-[0-9]{2}-[0-9]{2}T"
