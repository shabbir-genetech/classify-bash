module classify-bash

go 1.22

require (
	github.com/benhoyt/goawk v0.0.0-00010101000000-000000000000
	mvdan.cc/sh/v3 v3.10.0
)

replace github.com/benhoyt/goawk => github.com/shabbir-genetech/goawk v1.31.1-0.20260526050243-f51e03f1dd00
