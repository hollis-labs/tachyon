package main

import "regexp"

var activityRecipient = regexp.MustCompile(`^msg://(?:agent|session)/[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]{1,128}$`)
