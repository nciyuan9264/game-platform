package utils

// ================================
// 公司类型（替代字符串）
// ================================

type CompanyType int

const (
	Continental CompanyType = iota
	Imperial
	American
	Festival
	Worldwide
	Tower
	Sackson
)

// ================================
// 股票等级
// ================================

type StockLevel int

const (
	Premium StockLevel = iota
	Medium
	Low
)

// ================================
// 股票规则结构
// ================================

type StockInfo struct {
	MinTile     int
	MaxTile     int
	Price       int
	BonusFirst  int
	BonusSecond int
}

// ================================
// 三种等级规则表
// ================================

var premiumStock = []StockInfo{
	{0, 0, 0, 0, 0},
	{2, 2, 400, 4000, 2000},
	{3, 3, 500, 5000, 2500},
	{4, 4, 600, 6000, 3000},
	{5, 5, 700, 7000, 3500},
	{6, 10, 800, 8000, 4000},
	{11, 20, 900, 9000, 4500},
	{21, 30, 1400, 10000, 5000},
	{31, 40, 1700, 11000, 5500},
	{41, 1000, 2000, 12000, 6000},
}

var mediumStock = []StockInfo{
	{0, 0, 0, 0, 0},
	{2, 2, 300, 3000, 1500},
	{3, 3, 400, 4000, 2000},
	{4, 4, 500, 5000, 2500},
	{5, 5, 600, 6000, 3000},
	{6, 10, 700, 7000, 3500},
	{11, 20, 800, 8000, 4000},
	{21, 30, 1300, 9000, 4500},
	{31, 40, 1600, 10000, 5000},
	{41, 1000, 1900, 11000, 5500},
}

var lowStock = []StockInfo{
	{0, 0, 0, 0, 0},
	{2, 2, 200, 2000, 1000},
	{3, 3, 300, 3000, 1500},
	{4, 4, 400, 4000, 2000},
	{5, 5, 500, 5000, 2500},
	{6, 10, 600, 6000, 3000},
	{11, 20, 700, 7000, 3500},
	{21, 30, 1200, 8000, 4000},
	{31, 40, 1500, 9000, 4500},
	{41, 1000, 1800, 10000, 5000},
}

// ================================
// 公司 -> 等级映射
// ================================

var companyLevelMap = map[CompanyType]StockLevel{
	Continental: Premium,
	Imperial:    Premium,

	American:  Medium,
	Festival:  Medium,
	Worldwide: Medium,

	Tower:   Low,
	Sackson: Low,
}

// ================================
// 等级 -> 规则表
// ================================

var stockTable = map[StockLevel][]StockInfo{
	Premium: premiumStock,
	Medium:  mediumStock,
	Low:     lowStock,
}

func ParseCompanyType(name string) (CompanyType, bool) {
	switch name {
	case "Continental":
		return Continental, true
	case "Imperial":
		return Imperial, true
	case "American":
		return American, true
	case "Festival":
		return Festival, true
	case "Worldwide":
		return Worldwide, true
	case "Tower":
		return Tower, true
	case "Sackson":
		return Sackson, true
	default:
		return 0, false
	}
}

// ================================
// 查询函数
// ================================

func GetStockInfo(company CompanyType, tileCount int) *StockInfo {
	level, ok := companyLevelMap[company]
	if !ok {
		return nil
	}

	infos := stockTable[level]

	for i := range infos {
		info := &infos[i]
		if tileCount >= info.MinTile && tileCount <= info.MaxTile {
			return info
		}
	}

	return nil
}
