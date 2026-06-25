# Health check smoke test
# Verifies the server is running and responding.

GET {{HOST}}/healthz
HTTP 200
[Asserts]
jsonpath "$.ok" == true
header "Content-Type" == "application/json"
