resource "juju_application" "this" {
  name = "my-application"

  model = juju_model.development.name

  charm {
    name     = "hello-kubecon"
    channel  = "edge"
    revision = 14
    series   = "trusty"
  }

  units = 3

  config = {
    external-hostname = "..."
  }
}

resource "juju_application" "placement_and_storage_example" {
  name  = "placement-example"
  model = juju_model.development.name
  charm {
    name     = "hello-kubecon"
    channel  = "edge"
    revision = 14
    series   = "trusty"
  }

  units     = 3
  placement = "0,1,2"

  storage = {
    files = "101M"
  }

  config = {
    external-hostname = "..."
  }
}