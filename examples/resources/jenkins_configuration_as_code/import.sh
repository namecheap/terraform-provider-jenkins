# Import an existing controller section by its top-level JCasC section name.
# Only the section is imported: yaml is not read back from the controller, since
# the export holds resolved secrets and keys this resource does not manage. The
# first apply after import re-applies the configured yaml.
terraform import jenkins_configuration_as_code.jenkins jenkins
