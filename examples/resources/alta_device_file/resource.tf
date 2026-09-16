# Keep a script that lives in version control as the one the router is running.
# The router rebuilds /etc on every boot and every configuration push, so anything that
# has to survive belongs on its persistent filesystem.
resource "alta_device_file" "post_cfg" {
  path    = "/cfg/post-cfg.sh"
  content = file("${path.module}/files/post-cfg.sh")
  mode    = "0755"

  # An edit made directly on the router shows up in the next plan rather than
  # diverging quietly from the repository.
  drift_detection = "content"

  # This file is load-bearing: the router runs it after every boot and push. Removing
  # it from Terraform should stop managing it, not delete it.
  on_destroy = "keep"
}

# Hooks are installed by a loader the provider owns. For them to survive a reboot, the
# file above has to run it, so keep this line in the committed copy:
#
#   [ -x /cfg/tf.d/loader.sh ] && /cfg/tf.d/loader.sh
#
# The hook resource reports when it is missing.

resource "alta_device_file" "vpn_conf" {
  path    = "/cfg/wg-pbr/vpn.conf"
  content = file("${path.module}/files/vpn.conf")
  mode    = "0644"
}
