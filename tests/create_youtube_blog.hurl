POST http://localhost:8080/v1/summaries
Content-Type: application/json

{
  "url": "https://www.youtube.com/watch?v=abc123def45",
  "format": "blog"
}

HTTP 202
[Asserts]
header "Content-Type" == "application/json"
jsonpath "$.status" == "queued"
