package warchest

var unitOrder = []UnitType{
	UnitSword,
	UnitPike,
	UnitCrossbow,
	UnitLight,
	UnitArcher,
	UnitCavalry,
	UnitLancer,
	UnitScout,
	UnitBerserker,
	UnitEnsign,
	UnitFootman,
	UnitKnight,
	UnitMarshall,
	UnitMercenary,
	UnitGuard,
	UnitPriest,
}

var unitTotals = map[UnitType]int{
	UnitSword:     5,
	UnitPike:      4,
	UnitCrossbow:  5,
	UnitLight:     5,
	UnitArcher:    4,
	UnitCavalry:   4,
	UnitLancer:    4,
	UnitScout:     5,
	UnitBerserker: 5,
	UnitEnsign:    5,
	UnitFootman:   5,
	UnitKnight:    4,
	UnitMarshall:  5,
	UnitMercenary: 5,
	UnitGuard:     5,
	UnitPriest:    4,
	UnitRoyal:     1,
}

var recommendedRosters = map[Side][]UnitType{
	SideOak: {
		UnitSword,
		UnitPike,
		UnitCrossbow,
		UnitLight,
	},
	SideFalcon: {
		UnitArcher,
		UnitCavalry,
		UnitLancer,
		UnitScout,
	},
}

func validUnit(unit UnitType) bool {
	if unit == UnitRoyal {
		return false
	}
	_, ok := unitTotals[unit]
	return ok
}

func validRoster(roster []UnitType) bool {
	if len(roster) != 4 {
		return false
	}
	seen := map[UnitType]bool{}
	for _, unit := range roster {
		if !validUnit(unit) || seen[unit] {
			return false
		}
		seen[unit] = true
	}
	return true
}

func validRosters(first, second []UnitType) bool {
	if !validRoster(first) || !validRoster(second) {
		return false
	}
	seen := map[UnitType]bool{}
	for _, roster := range [][]UnitType{first, second} {
		for _, unit := range roster {
			if seen[unit] {
				return false
			}
			seen[unit] = true
		}
	}
	return true
}

func copyUnits(values []UnitType) []UnitType {
	return append([]UnitType(nil), values...)
}

func containsUnit(values []UnitType, target UnitType) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
