data "togetherai_hardware" "all" {}

# Hourly price of one replica, in dollars, per hardware type.
output "hourly_price" {
  value = { for h in data.togetherai_hardware.all.hardware : h.id => h.price_cents_per_hour / 100 }
}
