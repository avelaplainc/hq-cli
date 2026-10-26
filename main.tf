output "mock" { value = { location = var.location, prefix = var.resource_prefix, tags = var.tags, instances = var.instances, tls = var.enable_tls } }
