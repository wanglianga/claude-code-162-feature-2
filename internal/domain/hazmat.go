package domain

// 不可回收物(危险/有害废物)混入处理的领域规则。
//
// 背景: 居民把油漆桶混在金属中。回收员现场识别后:
//  1. 标记不可回收物并说明风险(risk_level + risk_note);
//  2. 居民坚持要求带走时, 服务给出拒收依据(refusal basis, 法规条款级);
//  3. 同步给出正确投放建议与附近处理点。
// 若回收员误收, 站点入库时登记环保风险与处理成本, 异常扣款从订单收益或培训记录体现。

// 环保风险等级
const (
	RiskLow    = "LOW"
	RiskMedium = "MEDIUM"
	RiskHigh   = "HIGH"
)

// 拒收依据编码
const (
	// BasisNationalHWList 《国家危险废物名录》: 沾染危废的废弃包装物属危险废物
	BasisNationalHWList = "NATIONAL_HW_LIST"
	// BasisNoLicense 回收主体无危废经营许可(固废法)
	BasisNoLicense = "NO_HAZMAT_LICENSE"
	// BasisContamination 混入会污染整批可回收物
	BasisContamination = "BATCH_CONTAMINATION"
	// BasisLocalSorting 地方生活垃圾管理条例: 有害垃圾须专投
	BasisLocalSorting = "LOCAL_SORTING_RULE"
)

// 误扣扣款去向
const (
	// DeductFromOrder 从对应订单的回收员收益中扣减
	DeductFromOrder = "ORDER"
	// DeductFromTraining 计入回收员培训/再教育记录
	DeductFromTraining = "TRAINING"
)

// 拒收记录状态
const (
	RejectionRejected  = "REJECTED"   // 拒收, 不予带走
	RejectionTakenBack = "TAKEN_BACK" // 居民自行带回, 按建议另行处理
)

// 培训记录状态
const (
	TrainingOpen = "OPEN" // 待培训
	TrainingDone = "DONE"
)

// HazmatInfo 不可回收物档案(目录)
type HazmatInfo struct {
	Code       string   // 不可回收物编码
	Name       string   // 名称
	RiskLevel  string   // 风险等级
	Categories []string // 通常被混入的可回收品类
	// 适用拒收依据(首个为主要依据)
	Bases []string
	// 风险说明(回收员现场向居民解释)
	RiskNote string
	// 正确投放建议
	Advice string
}

// refusalBasisText 拒收依据的可读文本(法规/标准条款)
var refusalBasisText = map[string]string{
	BasisNationalHWList: "《国家危险废物名录》(2021年版): 含有或沾染危险废物的废弃包装物、容器属危险废物" +
		"(HW49 其他废物, 废物代码 900-041-49), 不得通过再生资源回收渠道交售",
	BasisNoLicense: "《中华人民共和国固体废物污染环境防治法》第八十条: 从事危险废物收集、贮存、处置经营活动须取得危险废物经营许可证; " +
		"本回收服务及回收员无危废经营资质, 依法不得接收",
	BasisContamination: "桶内残留油漆/有机溶剂易燃且含挥发性有机物、重金属, 混入金属会污染整批回收物, " +
		"导致整批退运并增加环境泄漏风险",
	BasisLocalSorting: "《上海市生活垃圾管理条例》: 废油漆桶及其残留物属于有害垃圾, 须投放至有害垃圾专用收集容器或交由有资质的处理点",
}

// hazmatCatalog 不可回收物目录(回收员现场可标记的常见品类)
var hazmatCatalog = map[string]HazmatInfo{
	"paint_bucket": {
		Code: "paint_bucket", Name: "油漆桶", RiskLevel: RiskHigh, Categories: []string{CatMetal},
		Bases:    []string{BasisNationalHWList, BasisNoLicense, BasisContamination, BasisLocalSorting},
		RiskNote: "残留油漆属易燃危险废物, 含苯系物等挥发性有机物及铅、铬等重金属; 混入金属冶炼会产生有毒烟气并污染整批废料",
		Advice:   "请尽量倒空并在通风处晾干(勿倒入下水道), 不要与金属、塑料混投; 整桶加盖后投放至小区有害垃圾收集点, 或送至附近有危废资质的处理点",
	},
	"used_oil_drum": {
		Code: "used_oil_drum", Name: "废机油桶", RiskLevel: RiskHigh, Categories: []string{CatMetal},
		Bases:    []string{BasisNationalHWList, BasisNoLicense, BasisContamination},
		RiskNote: "废矿物油及其沾染包装物属危险废物(HW08), 易燃且污染土壤水体",
		Advice:   "保持桶盖密封、防止残油泄漏, 交机动车维修企业(危废产生单位)或有资质的废矿物油处理点回收",
	},
	"lead_battery": {
		Code: "lead_battery", Name: "废铅蓄电池", RiskLevel: RiskHigh, Categories: []string{CatMetal, CatAppliance},
		Bases:    []string{BasisNationalHWList, BasisNoLicense, BasisContamination},
		RiskNote: "废铅蓄电池属危险废物(HW31), 含铅与硫酸, 破损会造成重金属污染和化学灼伤",
		Advice:   "保持直立、避免破损短路, 交至电动车/蓄电池销售门店以旧换新点或持证危废处理点",
	},
	"fluorescent_lamp": {
		Code: "fluorescent_lamp", Name: "废日光灯管", RiskLevel: RiskHigh, Categories: []string{CatMetal, CatAppliance},
		Bases:    []string{BasisLocalSorting, BasisNationalHWList},
		RiskNote: "灯管含汞, 破碎后汞蒸气污染空气, 属有害垃圾",
		Advice:   "整根妥善包裹防碎, 投放至有害垃圾收集点; 节能灯、水银温度计同理",
	},
	"pesticide_container": {
		Code: "pesticide_container", Name: "农药/杀虫剂瓶", RiskLevel: RiskHigh, Categories: []string{CatPlastic},
		Bases:    []string{BasisNationalHWList, BasisNoLicense},
		RiskNote: "沾染农药的包装物属危险废物(HW49), 残留药剂有毒, 混入塑料会污染再生料",
		Advice:   "不要清洗后混投塑料, 整瓶交至农资销售门店回收点或乡镇有害垃圾归集点",
	},
	"expired_medicine": {
		Code: "expired_medicine", Name: "过期药品", RiskLevel: RiskMedium, Categories: []string{},
		Bases:    []string{BasisLocalSorting},
		RiskNote: "过期药品及其包装属有害垃圾, 随意丢弃会污染土壤水源并存在误食风险",
		Advice:   "连同包装交至药店过期药品回收箱或社区有害垃圾收集点",
	},
}

// LookupHazmat 查询不可回收物目录
func LookupHazmat(code string) (HazmatInfo, bool) {
	h, ok := hazmatCatalog[code]
	return h, ok
}

// RefusalBasisText 拒收依据编码 -> 可读条款文本
func RefusalBasisText(basis string) string {
	return refusalBasisText[basis]
}

// PrimaryRefusal 返回主要拒收依据编码与条款文本
func (h HazmatInfo) PrimaryRefusal() (code, text string) {
	if len(h.Bases) == 0 {
		return "", ""
	}
	return h.Bases[0], refusalBasisText[h.Bases[0]]
}

// AllRefusalBases 返回全部适用依据(编码+文本), 居民坚持要求带走时逐条展示
func (h HazmatInfo) AllRefusalBases() []map[string]string {
	out := make([]map[string]string, 0, len(h.Bases))
	for _, b := range h.Bases {
		out = append(out, map[string]string{"code": b, "text": refusalBasisText[b]})
	}
	return out
}

// CanMixInCategory 该不可回收物是否常被混入指定可回收品类(用于现场校验提示)
func (h HazmatInfo) CanMixInCategory(category string) bool {
	for _, c := range h.Categories {
		if c == category {
			return true
		}
	}
	return false
}
