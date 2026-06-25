# 404 tests for non-existent run IDs
# Uses a valid UUID format so the router matches the path param,
# but the UUID has no corresponding row in the database.

# 1. Status endpoint with unknown run ID
GET {{HOST}}/v1/runs/00000000-0000-0000-0000-000000000000/status
HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"
jsonpath "$.error.message" == "run not found"

# 2. Summary endpoint with unknown run ID
GET {{HOST}}/v1/summaries/00000000-0000-0000-0000-000000000000
HTTP 404
[Asserts]
jsonpath "$.error.code" == "not_found"
jsonpath "$.error.message" == "run not found"
