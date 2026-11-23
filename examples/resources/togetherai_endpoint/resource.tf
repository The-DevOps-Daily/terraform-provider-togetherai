resource "togetherai_endpoint" "chat" {
  name = "chat"
}

# Clients send requests with model = this value.
output "endpoint_name" {
  value = togetherai_endpoint.chat.qualified_name
}
