resource "jenkins_credential_certificate" "example" {
  name     = "my-certificate"
  keystore = filebase64("${path.module}/keystore.p12")
  password = var.keystore_password
}
