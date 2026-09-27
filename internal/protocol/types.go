// Package protocol 定义与 Yearning web 端之间 net/rpc(gob) 传输的全部结构。
//
// 字段名与 Yearning v3.1.x 公开源码保持一致（gob 按字段名匹配：
// 发送方多余字段被忽略，接收方可以只镜像需要的字段）。
package protocol

// Level 语义（Yearning 前端以 level === 0 判定阻断提交）：
// 0 = 错误（阻断），1 = 警告（放行），2 = 通过
const (
	LevelError   uint8 = 0
	LevelWarning uint8 = 1
	LevelPass    uint8 = 2
)

// AuditRole —— Yearning src/engine/engine.go 的审核规则集
type AuditRole struct {
	DMLTransaction                 bool
	DMLAllowLimitSTMT              bool
	DMLInsertColumns               bool
	DMLMaxInsertRows               int
	DMLWhere                       bool
	DMLWhereExprValueIsNull        bool
	DMLOrder                       bool
	DMLSelect                      bool
	DMLAllowInsertNull             bool
	DMLInsertMustExplicitly        bool
	DDLEnablePrimaryKey            bool
	DDLCheckTableComment           bool
	DDlCheckColumnComment          bool
	DDLCheckColumnNullable         bool
	DDLCheckColumnDefault          bool
	DDLEnableAcrossDBRename        bool
	DDLEnableAutoincrementInit     bool
	DDLEnableAutoIncrement         bool
	DDLEnableAutoincrementUnsigned bool
	DDLEnableDropTable             bool
	DDLEnableDropDatabase          bool
	DDLEnableNullIndexName         bool
	DDLIndexNameSpec               bool
	DDLMaxKeyParts                 uint
	DDLMaxKey                      uint
	DDLMaxCharLength               uint
	MaxTableNameLen                int
	MaxAffectRows                  uint
	MaxDDLAffectRows               uint
	SupportCharset                 string
	SupportCollation               string
	CheckIdentifier                bool
	MustHaveColumns                string
	DDLMultiToCommit               bool
	DDLPrimaryKeyMust              bool
	DDLAllowColumnType             bool
	DDLImplicitTypeConversion      bool
	DDLAllowPRINotInt              bool
	DDLAllowMultiAlter             bool
	DDLEnableForeignKey            bool
	DDLTablePrefix                 string
	DDLColumnsMustHaveIndex        string
	DDLAllowChangeColumnPosition   bool
	DDLCheckFloatDouble            bool
	IsOSC                          bool
	OSCExpr                        string
	OscSize                        uint
	AllowCreateView                bool
	AllowCrateViewWithSelectStar   bool
	AllowCreatePartition           bool
	AllowSpecialType               bool
	PRIRollBack                    bool
}

// Record —— Yearning src/engine/engine.go（isOSC 为未导出字段，gob 不传输）
type Record struct {
	SQL              string
	AffectRows       uint
	Status           string
	Error            string
	Level            uint8
	ExecTime         string
	Table            string
	Schema           string
	InsulateWordList []string
}

// CheckArgs —— Yearning src/engine/engine.go
type CheckArgs struct {
	SQL      string
	Schema   string
	Kind     int // 0 = DDL 工单，1 = DML 工单（与 CoreSqlOrder.Type 一致）
	Lang     string
	Rule     AuditRole
	IP       string
	Username string
	Port     int
	Password string
	CA       string
	Cert     string
	Key      string
}

// CoreSqlOrder —— Yearning src/model/modal.go（仅引擎用到的字段）
type CoreSqlOrder struct {
	ID          uint
	WorkId      string
	Username    string
	Status      uint
	Type        int // 1 dml 0 ddl
	Backup      uint
	IDC         string
	Source      string
	SourceId    string
	DataBase    string
	Table       string
	Date        string
	SQL         string
	Text        string
	Assigned    string
	Delay       string
	RealName    string
	ExecuteTime string
	CurrentStep int
	Relevant    []byte
	OSCInfo     string
	File        string
}

// Message —— Yearning src/model/subModel.go
type Message struct {
	WebHook  string
	Host     string
	Port     int
	User     string
	Password string
	ToUser   string
	Mail     bool
	Ding     bool
	Ssl      bool
	PushType bool
	Key      string
}

// ExecArgs —— Yearning src/handler/order/audit/impl.go
type ExecArgs struct {
	Order         *CoreSqlOrder
	Rules         AuditRole
	IP            string
	Port          int
	Username      string
	Password      string
	CA            string
	Cert          string
	Key           string
	Message       Message
	MaxAffectRows uint
}

// QueryArgs —— Yearning src/handler/personal/impl.go
type QueryArgs struct {
	SQL              string
	Limit            uint64
	InsulateWordList string
}

// Confirm —— Yearning src/handler/order/audit/impl.go
type Confirm struct {
	WorkId   string
	Page     int
	Flag     int
	Text     string
	Tp       string
	SourceId string
	Delay    string
}

// 工单状态（core_sql_orders.status，引擎回写用）：
// 0=已驳回 2=待执行 3=完成 4=执行中 5=失败 6=撤销
const (
	OrderStatusSuccess = 3
	OrderStatusFailed  = 5
)
