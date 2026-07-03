resource "jenkins_user" "developer" {
  username  = "jdoe"
  password  = var.jdoe_password # keep secrets out of source control
  full_name = "Jane Doe"
  email     = "jane.doe@example.com"
}
