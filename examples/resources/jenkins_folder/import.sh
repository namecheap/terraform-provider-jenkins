# Folders are imported by their canonical path.
terraform import jenkins_folder.team /job/platform-team

# A nested folder includes every parent in the path.
terraform import jenkins_folder.squad /job/platform-team/job/backend-squad
