// Package mqttperm exercises the UV0002 reverse check: mqtt.pub:/mqtt.sub:
// grants in config.toml [auth.roles] must be well-formed MQTT topic filters.
// Malformed filters are dead grants; the lint anchors at the package clause
// (config grants have no Go position) and names the offending grant. Valid
// filters and broad/non-mqtt grants are never flagged.
package mqttperm // want `permission "mqtt.pub:" in config.toml \[auth.roles\] is not a well-formed MQTT topic filter: the filter is empty` `permission "mqtt.pub:sport/#/player" .*"#" is not the last level` `permission "mqtt.sub:sp\+rt" .*shares a level with other characters`
