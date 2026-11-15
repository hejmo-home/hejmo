// Package install holds the tests of install.sh (repository root), the install
// script for Raspberry Pi OS (ADR 0018), and of the install guide. The script
// is exercised as a black box in a fake root with stand-ins for docker,
// systemctl, apt-get and curl, so nothing on the developer's machine changes.
package install
