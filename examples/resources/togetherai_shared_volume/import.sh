# Import a shared volume by its volume ID. Set region in the configuration to
# the volume's real region: the API does not return it.
terraform import togetherai_shared_volume.datasets <volume_id>
