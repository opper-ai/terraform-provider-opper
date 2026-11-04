data "opper_member" "developer" {
  email = "developer@example.com"
}

output "developer_member_uuid" {
  value = data.opper_member.developer.id
}
