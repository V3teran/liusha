package finding

import "strings"

// ClassInfo 是发现类型词表的一项。
type ClassInfo struct {
	Code string // 存进 finding.class 的取值（小写 snake_case），评估者坐实时按场景的词表给出
	Name string // 展示名
	// CWE 是这一类型对应的弱点编号（如 CWE-89）；没有合适编号的为空。按类型固定映射、不由 LLM
	// 给出：LLM 常编造编号，扫描器的惯例也是每种问题对应一个固定的 CWE。
	CWE string
}

// ClassOther 是词表之外与未给出类型的发现的归类。
const ClassOther = "other"

// classes 是发现类型的封闭词表：台账的类型只取这里的值，图表、筛选与展示名都据此。内置的
// 「渗透测试」「CTF」场景向评估者列出同一份词表（internal/builtin 的场景正文，测试守住两边一致）。
// 产品按渗透测试定位，词表固定在台账层；场景被用户改写出新类别时，那些类别归入「其他」。
var classes = []ClassInfo{
	{Code: "reflected_xss", Name: "反射型 XSS", CWE: "CWE-79"},
	{Code: "stored_xss", Name: "存储型 XSS", CWE: "CWE-79"},
	{Code: "dom_xss", Name: "DOM 型 XSS", CWE: "CWE-79"},
	{Code: "sql_injection", Name: "SQL 注入", CWE: "CWE-89"},
	{Code: "command_injection", Name: "命令注入", CWE: "CWE-78"},
	{Code: "ssti", Name: "模板注入（SSTI）", CWE: "CWE-1336"},
	{Code: "path_traversal", Name: "路径穿越", CWE: "CWE-22"},
	{Code: "arbitrary_file_upload", Name: "任意文件上传", CWE: "CWE-434"},
	{Code: "ssrf", Name: "SSRF", CWE: "CWE-918"},
	{Code: "auth_bypass", Name: "认证绕过", CWE: "CWE-287"},
	{Code: "access_control", Name: "访问控制缺陷", CWE: "CWE-285"},
	{Code: "info_disclosure", Name: "敏感信息泄露", CWE: "CWE-200"},
	// 安全配置缺陷涵盖面太广，没有一个弱点编号能如实代表它（CWE-16 是已不许映射的分类）
	{Code: "security_misconfiguration", Name: "安全配置缺陷"},
	{Code: "weak_credential", Name: "弱口令", CWE: "CWE-1391"},
	{Code: "deserialization", Name: "反序列化", CWE: "CWE-502"},
	{Code: "xxe", Name: "XXE", CWE: "CWE-611"},
	{Code: "csrf", Name: "CSRF", CWE: "CWE-352"},
	{Code: "flag", Name: "Flag"},
	{Code: ClassOther, Name: "其他"},
}

var classByCode = func() map[string]ClassInfo {
	m := make(map[string]ClassInfo, len(classes))
	for _, c := range classes {
		m[c.Code] = c
	}
	return m
}()

// Classes 返回发现类型词表（按定义顺序）。
func Classes() []ClassInfo {
	return append([]ClassInfo(nil), classes...)
}

// NormalizeClass 把评估者给出的类型收进词表：词表内的取值原样（大小写与空白从宽），
// 词表之外的与空值归入「其他」。
func NormalizeClass(raw string) string {
	code := strings.ToLower(strings.TrimSpace(raw))
	if _, ok := classByCode[code]; ok {
		return code
	}
	return ClassOther
}

// Class 取一个类型的词表项；不在词表里的按「其他」。
func Class(code string) ClassInfo {
	if c, ok := classByCode[code]; ok {
		return c
	}
	return classByCode[ClassOther]
}
