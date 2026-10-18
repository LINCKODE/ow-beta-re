package heroes

import "testing"

func TestAllHeroes(t *testing.T) {
	if len(All) != 21 {
		t.Fatalf("%d heroes, want the 21 of 0.8", len(All))
	}
	for _, h := range All {
		st, ok := Start(h.Def)
		if !ok {
			t.Fatalf("%s: no start-up", h.Name)
		}
		weapons := 0
		for _, u := range h.Graphs {
			if Graphs[u.Graph] == nil {
				t.Errorf("%s: graph %#x has no layout", h.Name, u.Graph)
			}
			if u.Source == "weapon" {
				weapons++
			}
		}
		if weapons == 0 || len(st.Weapons) != weapons {
			t.Errorf("%s: %d weapon scripts, %d weapon indices", h.Name, weapons, len(st.Weapons))
		}
		seen := map[uint16]bool{}
		for i, in := range st.Instances {
			if in.ID != uint16(i+1) || seen[in.ID] {
				t.Fatalf("%s: instance %d has id %d", h.Name, i, in.ID)
			}
			seen[in.ID] = true
			if in.Parent != 0 && !seen[in.Parent] {
				t.Errorf("%s: instance %d's parent %d comes later", h.Name, in.ID, in.Parent)
			}
			for _, a := range in.Active {
				if a.State >= len(in.Graph.States) {
					t.Errorf("%s: graph %#x state %d out of range", h.Name, in.Graph.Index, a.State)
				}
			}
		}
	}
}

// Genji (def 0x225): the 12 overrides and the layouts read from the live client (T016 report,
// "Stage 2, parts C/D"; server/internal/instance heroScripts).
func TestGenji(t *testing.T) {
	st, ok := Start(0x225)
	if !ok || st.Hero.Name != "Genji" {
		t.Fatal("no Genji")
	}
	want := []uint32{0x033, 0x04b, 0x043, 0x157, 0x317, 0x1db, 0x3a3, 0x015, 0x304}
	for i, g := range want {
		if st.Instances[i].Graph.Index != g || st.Instances[i].Parent != 0 {
			t.Fatalf("instance %d: graph %#x, want %#x", i+1, st.Instances[i].Graph.Index, g)
		}
	}
	vars := func(i int) []Var { return Final(st.Instances[i].Vars) }
	check := func(i int, id uint32, want Value) {
		t.Helper()
		v, ok := Lookup(vars(i), id)
		if !ok || v != want {
			t.Errorf("instance %d $%03x = %+v, want %+v", i+1, id, v, want)
		}
	}
	check(0, 0x0c7, Int(200))
	check(0, 0x0dd, Int(0))
	check(1, 0x152, Int(1200))
	check(1, 0x3d6, Int(1))
	check(1, 0x769, Int64(0x09a00000000000fe)) // the HUD screen
	check(1, 0x70f, Int64(0x0c00000000002292)) // portrait
	check(1, 0x8ed, Byte(1))
	check(1, 0x8ee, Int(3))
	check(1, 0x8ef, Float(0.25))
	check(2, 0x2a3, Int64(0x012000000000051b))
	check(8, VarWeaponIndex, Int(1))
	// Hub entry #0 copies the ult cost into the entity bag.
	if v, ok := Lookup(st.Owner, 0x152); !ok || v != Int(1200) {
		t.Errorf("owner $152 = %+v", v)
	}
	// The hub's entries begin 14 states, 13 of them SubScripts: T016's recording of a client-started
	// hero (0xb2a included, behind a condition on owner $94c, which the client sets on its local hero
	// and the start-up now carries; 0x1cb is on the other branch).
	hub := st.Instances[1]
	subs := 0
	for _, a := range hub.Active {
		if a.Data == DataSubScript {
			subs++
			c := st.Instances[a.Child-1]
			if c.Parent != hub.ID || c.Slot != a.State {
				t.Errorf("child %d: parent %d slot %d, want %d / %d", c.ID, c.Parent, c.Slot, hub.ID, a.State)
			}
		}
	}
	if len(hub.Active) != 14 || subs != 13 {
		t.Errorf("hub: %d active, %d SubScripts", len(hub.Active), subs)
	}
	// Child 0x1cf (hub state 3) gets the HUD screen through the SubScript init data, and its entry
	// begins the UXScreen state 2 that shows it (through a NoOp).
	var hud Instance
	for _, in := range st.Instances {
		if in.Graph.Index == 0x1cf {
			hud = in
		}
	}
	if v, ok := Lookup(hud.Vars, 0x769); hud.Graph.Index != 0x1cf || !ok || v != Int64(0x09a00000000000fe) {
		t.Errorf("0x1cf: $769 = %+v", v)
	}
	if !slicesContains(hud.Active, 2) {
		t.Errorf("0x1cf state 2 not active: %v", hud.Active)
	}
	// Weapon selection (T044): the manager's Watch picks weapon 1, so the weapon script's "current
	// weapon" switch (0x304 #0: $1f == $1c) begins true, with its subgraph states.
	if v, ok := Lookup(st.Owner, VarCurrentWeapon); !ok || v != Int(1) {
		t.Errorf("owner $1f = %+v", v)
	}
	weapon := st.Instances[8]
	if weapon.Graph.Index != 0x304 || len(weapon.Active) < 2 || weapon.Active[0].State != 0 || !weapon.Active[0].Flag {
		t.Errorf("weapon 0x304: %+v", weapon.Active)
	}
	g := Graphs[0x033]
	if g.States != "...VV...CCCVC." || len(g.SyncVars) != 4 || len(g.RemoteStates) != 5 {
		t.Errorf("0x033 layout %q %d %d", g.States, len(g.SyncVars), len(g.RemoteStates))
	}
}

// Soldier: 76 (def 0x3cf), the tutorial's hero.
func TestSoldier(t *testing.T) {
	st, ok := Start(0x3cf)
	if !ok || st.Hero.Name != "Soldier: 76" {
		t.Fatal("no Soldier: 76")
	}
	if v, ok := Lookup(Final(st.Instances[1].Vars), 0x152); !ok || v != Int(1500) {
		t.Errorf("hub $152 = %+v", v)
	}
	if h, _ := ByHero(0x02E000000000006E); h == nil || h.Def != 0x3cf {
		t.Error("ByHero")
	}
}

func slicesContains(as []Active, state int) bool {
	for _, a := range as {
		if a.State == state {
			return true
		}
	}
	return false
}
