data "opper_members" "team" {}

output "member_ids" {
  value = [for member in data.opper_members.team.members : member.uuid]
}
