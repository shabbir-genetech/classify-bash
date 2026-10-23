package main

import "github.com/shabbir-genetech/classify-bash/internal/engine"

// fsHost is the real osHost with the environment pinned empty, so the tables
// do not depend on what the suite runs in; the filesystem checks are real.
type fsHost struct{ osHost }

func (fsHost) EnvSet(string) bool { return false }

func classifyIn(cmd, dir string) engine.Class {
	return engine.ClassifyWith(cmd, fsHost{osHost{scratch: dir}}).Class
}
