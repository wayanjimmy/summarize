POST http://localhost:8080/v1/summaries
Content-Type: application/json

{
  "text": "Some long text for testing format validation",
  "format": "chapters"
}

HTTP 400
[Asserts]
header "Content-Type" == "application/json"
jsonpath "$.error.code" == "invalid_request"
jsonpath "$.error.message" contains "chapters"
