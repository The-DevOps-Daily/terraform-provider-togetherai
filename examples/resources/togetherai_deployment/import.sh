# Import a deployment by endpoint ID and deployment ID...
terraform import togetherai_deployment.stable <endpoint_id>/<deployment_id>

# ...or with the project too.
terraform import togetherai_deployment.stable <project_id>/<endpoint_id>/<deployment_id>
