package sql

// exactTextEqual compares text bytes independently of case/accent/padding
// collation. expression must be a trusted column identifier from source code;
// all request values remain bound through the single placeholder.
func exactTextEqual(driverName, expression string) string {
	switch driverName {
	case "mysql":
		return "CAST(" + expression + " AS BINARY) = CAST(? AS BINARY)"
	case "postgres":
		return "convert_to(" + expression + ", 'UTF8') = convert_to(CAST(? AS text), 'UTF8')"
	default:
		return "CAST(" + expression + " AS BLOB) = CAST(? AS BLOB)"
	}
}
