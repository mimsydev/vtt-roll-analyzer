package engine

import (
	"time"
)

// The structs needed for data organization in the readers
type RollEvent struct {
	Timestamp  time.Time
	Roller     Roller     // who rolled — see struct below
	Formula    string     // e.g. "1d20+5"
	Result     int        // total after modifiers
	Rolls      []int      // individual die faces — required for crit/fumble stats
	Label      string     // free-text purpose, e.g. "Roll for Initiative"
	Visibility Visibility // see enum below — replaces earlier boolean IsGM
	Source     string     // originating file/session identifier
}

type Roller struct {
	Name string
	Role Role
}

type Role string

const (
	RoleGM      Role = "gm"
	RolePlayer  Role = "player"
	RoleUnknown Role = "unknown" // reader couldn't determine this from the source
)

type Visibility string

const (
	VisibilityPublic      Visibility = "public"
	VisibilityGMRoll      Visibility = "gmroll"      // roller + GM see result
	VisibilitySecret      Visibility = "secret"      // GM only; roller gets confirmation, no value
	VisibilitySuperSecret Visibility = "supersecret" // GM only; roller sees nothing
)
