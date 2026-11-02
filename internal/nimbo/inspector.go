package nimbo

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ---- DTOs ----

type BucketDto struct {
	Name    string  `json:"name"`
	Created *string `json:"created"`
}
type S3ObjectDto struct {
	Key          string  `json:"key"`
	Size         int64   `json:"size"`
	LastModified *string `json:"lastModified"`
}
type ObjectListDto struct {
	Bucket    string        `json:"bucket"`
	Objects   []S3ObjectDto `json:"objects"`
	Truncated bool          `json:"truncated"`
}
type ObjectPreviewDto struct {
	Bucket    string `json:"bucket"`
	Key       string `json:"key"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

type QueueDto struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Messages *int   `json:"messages"`
	InFlight *int   `json:"inFlight"`
	Delayed  *int   `json:"delayed"`
}
type QueueMessageDto struct {
	ID           string `json:"id"`
	Body         string `json:"body"`
	ReceiveCount *int   `json:"receiveCount"`
	SentAt       *int64 `json:"sentAt"`
}
type QueueMessagesDto struct {
	Queue    string            `json:"queue"`
	Messages []QueueMessageDto `json:"messages"`
}

type TableDto struct {
	Name   string   `json:"name"`
	Status *string  `json:"status"`
	Items  *int64   `json:"items"`
	Keys   []string `json:"keys"`
}
type ItemsDto struct {
	Table string `json:"table"`
	Items []any  `json:"items"`
	Count int    `json:"count"`
}

type TopicDto struct {
	Name string `json:"name"`
}
type SubscriptionDto struct {
	Name  string  `json:"name"`
	Topic *string `json:"topic"`
}
type PubSubDto struct {
	Project       string            `json:"project"`
	Topics        []TopicDto        `json:"topics"`
	Subscriptions []SubscriptionDto `json:"subscriptions"`
}

// Defaults the Kotlin methods had as default arguments, for callers that do not care.
const (
	InspectObjectsMax      = 200
	InspectPreviewBytes    = 4096
	InspectItemsLimit      = 25
	InspectDefaultAccount  = "000000000000"
	InspectDefaultProject  = "floci-local"
	inspectDefaultAWSURL   = "http://localhost:4566"
	inspectDefaultGCPURL   = "http://localhost:4588"
	inspectPlaceholderDate = "20260101"
)

var (
	inspectName    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,255}$`)
	inspectProject = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	inspectCode    = regexp.MustCompile(`<Code>([^<]{1,80})</Code>`)
)

// Inspector gives read-only views over what is stored in the local emulators. The emulators do not check
// signatures, so a fixed placeholder Authorization header is enough and no credentials are involved.
type Inspector struct {
	gw  HTTPGateway
	aws string
	gcp string
}

// NewInspector talks to the AWS emulator at awsEndpoint and the GCP one at gcpEndpoint; empty means the
// localhost defaults (4566 and 4588).
func NewInspector(gw HTTPGateway, awsEndpoint, gcpEndpoint string) *Inspector {
	return &Inspector{gw: gw, aws: orDefault(awsEndpoint, inspectDefaultAWSURL), gcp: orDefault(gcpEndpoint, inspectDefaultGCPURL)}
}

// ---------- S3 ----------

func (i *Inspector) Buckets(ctx context.Context) ([]BucketDto, error) {
	root, err := i.xml(i.awsCall(ctx, "s3", "GET", "/", nil, nil))
	if err != nil {
		return nil, err
	}
	out := []BucketDto{}
	for _, b := range root.elements("Bucket") {
		out = append(out, BucketDto{Name: b.textOr("Name"), Created: b.textPtr("CreationDate")})
	}
	return out, nil
}

// Objects lists up to max keys (1..1000) under prefix.
func (i *Inspector) Objects(ctx context.Context, bucket, prefix string, max int) (ObjectListDto, error) {
	if err := inspectCheckName(bucket, "bucket"); err != nil {
		return ObjectListDto{}, err
	}
	query := "list-type=2&max-keys=" + strconv.Itoa(obsClamp(max, 1, 1000))
	if prefix != "" {
		query += "&prefix=" + inspectEnc(prefix)
	}
	root, err := i.xml(i.awsCall(ctx, "s3", "GET", "/"+bucket+"?"+query, nil, nil))
	if err != nil {
		return ObjectListDto{}, err
	}
	objects := []S3ObjectDto{}
	for _, c := range root.elements("Contents") {
		var size int64
		if s, ok := c.text("Size"); ok {
			size, _ = strconv.ParseInt(s, 10, 64)
		}
		objects = append(objects, S3ObjectDto{Key: c.textOr("Key"), Size: size, LastModified: c.textPtr("LastModified")})
	}
	truncated, _ := root.text("IsTruncated")
	return ObjectListDto{Bucket: bucket, Objects: objects, Truncated: truncated == "true"}, nil
}

// Preview reads the first maxBytes of an object with a Range request.
func (i *Inspector) Preview(ctx context.Context, bucket, key string, maxBytes int) (ObjectPreviewDto, error) {
	if err := inspectCheckName(bucket, "bucket"); err != nil {
		return ObjectPreviewDto{}, err
	}
	segments := strings.Split(key, "/")
	if key == "" {
		return ObjectPreviewDto{}, InvalidName{"Invalid object key"}
	}
	enc := make([]string, len(segments))
	for n, s := range segments {
		if s == "." || s == ".." {
			return ObjectPreviewDto{}, InvalidName{"Invalid object key"}
		}
		enc[n] = inspectEnc(s)
	}
	path := "/" + bucket + "/" + strings.Join(enc, "/")
	reply, err := i.awsCall(ctx, "s3", "GET", path, nil, map[string]string{"Range": fmt.Sprintf("bytes=0-%d", maxBytes-1)})
	if err != nil {
		return ObjectPreviewDto{}, err
	}
	body := []rune(reply.Body)
	text := body
	if len(text) > maxBytes {
		text = text[:max(maxBytes, 0)]
	}
	return ObjectPreviewDto{Bucket: bucket, Key: key, Text: string(text), Truncated: len(body) >= maxBytes}, nil
}

// ---------- SQS ----------

func (i *Inspector) Queues(ctx context.Context) ([]QueueDto, error) {
	form := "Action=ListQueues&Version=2012-11-05"
	root, err := i.xml(i.awsCall(ctx, "sqs", "POST", "/", &form, nil))
	if err != nil {
		return nil, err
	}
	var urls []string
	for _, q := range root.elements("QueueUrl") {
		if u := strings.TrimSpace(q.content()); u != "" {
			urls = append(urls, u)
		}
	}
	out := []QueueDto{}
	for _, u := range urls {
		account, name, err := inspectQueueParts(u)
		if err != nil {
			return nil, err
		}
		// A queue whose counters cannot be read still shows up, just without numbers.
		attrs, aerr := i.queueAttributes(ctx, account, name)
		if aerr != nil {
			attrs = nil
		}
		count := func(key string) *int {
			if n, ok := obsInt32(attrs[key]); ok {
				return &n
			}
			return nil
		}
		out = append(out, QueueDto{
			Name:     name,
			URL:      i.aws + "/" + account + "/" + name,
			Messages: count("ApproximateNumberOfMessages"),
			InFlight: count("ApproximateNumberOfMessagesNotVisible"),
			Delayed:  count("ApproximateNumberOfMessagesDelayed"),
		})
	}
	return out, nil
}

// Messages peeks with visibility timeout 0, so messages stay available (receive count still goes up, as on real
// SQS). An empty account means the emulator's default one.
func (i *Inspector) Messages(ctx context.Context, queue, account string) (QueueMessagesDto, error) {
	if account == "" {
		account = InspectDefaultAccount
	}
	if err := inspectCheckName(queue, "queue"); err != nil {
		return QueueMessagesDto{}, err
	}
	if err := inspectCheckName(account, "account"); err != nil {
		return QueueMessagesDto{}, err
	}
	form := "Action=ReceiveMessage&MaxNumberOfMessages=10&VisibilityTimeout=0&AttributeName.1=All&Version=2012-11-05"
	root, err := i.xml(i.awsCall(ctx, "sqs", "POST", "/"+account+"/"+queue, &form, nil))
	if err != nil {
		return QueueMessagesDto{}, err
	}
	messages := []QueueMessageDto{}
	for _, m := range root.elements("Message") {
		attrs := inspectAttributes(m)
		dto := QueueMessageDto{ID: m.textOr("MessageId"), Body: m.textOr("Body")}
		if n, err := strconv.ParseInt(attrs["ApproximateReceiveCount"], 10, 32); err == nil {
			c := int(n)
			dto.ReceiveCount = &c
		}
		if n, err := strconv.ParseInt(attrs["SentTimestamp"], 10, 64); err == nil {
			dto.SentAt = &n
		}
		messages = append(messages, dto)
	}
	return QueueMessagesDto{Queue: queue, Messages: messages}, nil
}

// inspectAttributes maps every <Attribute><Name/><Value/></Attribute> below n (the last one of a name wins).
func inspectAttributes(n *obsXMLNode) map[string]string {
	attrs := map[string]string{}
	for _, a := range n.elements("Attribute") {
		attrs[a.textOr("Name")] = a.textOr("Value")
	}
	return attrs
}

func (i *Inspector) queueAttributes(ctx context.Context, account, queue string) (map[string]any, error) {
	form := "Action=GetQueueAttributes&AttributeName.1=All&Version=2012-11-05"
	root, err := i.xml(i.awsCall(ctx, "sqs", "POST", "/"+account+"/"+queue, &form, nil))
	if err != nil {
		return nil, err
	}
	attrs := map[string]any{}
	for k, v := range inspectAttributes(root) {
		attrs[k] = v
	}
	return attrs, nil
}

// inspectQueueParts takes the account and name from a queue URL the emulator sent. Only these two parts are used,
// never the host, so a hostile reply cannot make Nimbo call another machine.
func inspectQueueParts(raw string) (account, name string, err error) {
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", "", EmulatorError{502, "Unexpected queue URL: " + raw}
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "", "", EmulatorError{502, "Unexpected queue URL: " + raw}
	}
	if err := inspectCheckName(parts[0], "account"); err != nil {
		return "", "", err
	}
	if err := inspectCheckName(parts[1], "queue"); err != nil {
		return "", "", err
	}
	return parts[0], parts[1], nil
}

// ---------- DynamoDB ----------

func (i *Inspector) Tables(ctx context.Context) ([]TableDto, error) {
	reply, err := i.dynamo(ctx, "ListTables", "{}")
	if err != nil {
		return nil, err
	}
	out := []TableDto{}
	for _, n := range obsArr(obsObj(reply).Get("TableNames")) {
		name, _ := obsText(n)
		nameJSON, _ := json.Marshal(name)
		dto := TableDto{Name: name, Keys: []string{}}
		// A table that cannot be described still shows up by name.
		if described, derr := i.dynamo(ctx, "DescribeTable", `{"TableName":`+string(nameJSON)+`}`); derr == nil {
			if table := obsObj(obsObj(described).Get("Table")); table != nil {
				if s, ok := obsText(table.Get("TableStatus")); ok {
					dto.Status = &s
				}
				if n, ok := obsInt64(table.Get("ItemCount")); ok {
					dto.Items = &n
				}
				for _, k := range obsArr(table.Get("KeySchema")) {
					if attr, ok := obsText(obsObj(k).Get("AttributeName")); ok {
						dto.Keys = append(dto.Keys, attr)
					}
				}
			}
		}
		out = append(out, dto)
	}
	return out, nil
}

// Items scans up to limit (1..100) items and flattens DynamoDB's typed values.
func (i *Inspector) Items(ctx context.Context, table string, limit int) (ItemsDto, error) {
	if err := inspectCheckName(table, "table"); err != nil {
		return ItemsDto{}, err
	}
	reply, err := i.dynamo(ctx, "Scan", fmt.Sprintf(`{"TableName":"%s","Limit":%d}`, table, obsClamp(limit, 1, 100)))
	if err != nil {
		return ItemsDto{}, err
	}
	items := []any{}
	for _, it := range obsArr(obsObj(reply).Get("Items")) {
		items = append(items, inspectSimplifyDynamo(it))
	}
	return ItemsDto{Table: table, Items: items, Count: len(items)}, nil
}

func (i *Inspector) dynamo(ctx context.Context, operation, body string) (any, error) {
	headers := inspectAuth("dynamodb")
	headers["X-Amz-Target"] = "DynamoDB_20120810." + operation
	headers["Content-Type"] = "application/x-amz-json-1.0"
	reply, err := i.gw.Send(ctx, "POST", i.aws+"/", headers, &body)
	if err != nil {
		return nil, err
	}
	if reply.Status < 200 || reply.Status > 299 {
		return nil, EmulatorError{reply.Status, "DynamoDB " + operation + " failed: " + obsTake(reply.Body, 200)}
	}
	v, perr := obsParse(reply.Body)
	if perr != nil {
		return nil, EmulatorError{502, "Unreadable JSON from emulator"}
	}
	return v, nil
}

// inspectSimplifyDynamo turns DynamoDB's typed attribute values ({"S":"x"}) into plain JSON.
func inspectSimplifyDynamo(value any) any {
	obj := obsObj(value)
	if obj == nil {
		return value
	}
	mapped := func() any {
		out := newObsObject()
		for _, k := range obj.keys {
			out.set(k, inspectSimplifyDynamo(obj.vals[k]))
		}
		return out
	}
	if obj.Len() != 1 {
		return mapped()
	}
	typ := obj.keys[0]
	inner := obj.vals[typ]
	switch typ {
	case "S", "B", "SS", "BS":
		return inner
	case "N":
		text, ok := obsText(inner)
		if !ok {
			return inner
		}
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(text, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
		return inner
	case "BOOL":
		switch b := inner.(type) {
		case bool:
			return b
		case string:
			return b == "true"
		}
		return false
	case "NULL":
		return nil
	case "L":
		arr, ok := inner.([]any)
		if !ok {
			return inner
		}
		out := make([]any, len(arr))
		for n, v := range arr {
			out[n] = inspectSimplifyDynamo(v)
		}
		return out
	case "NS":
		arr, ok := inner.([]any)
		if !ok {
			return inner
		}
		out := make([]any, len(arr))
		for n, v := range arr {
			typed := newObsObject()
			typed.set("N", v)
			out[n] = inspectSimplifyDynamo(typed)
		}
		return out
	case "M":
		m := obsObj(inner)
		if m == nil {
			return inner
		}
		out := newObsObject()
		for _, k := range m.keys {
			out.set(k, inspectSimplifyDynamo(m.vals[k]))
		}
		return out
	}
	return mapped()
}

// ---------- Pub/Sub (GCP) ----------

func (i *Inspector) Pubsub(ctx context.Context, project string) (PubSubDto, error) {
	if !inspectProject.MatchString(project) {
		return PubSubDto{}, InvalidName{"Invalid project id"}
	}
	list := func(kind string) (*obsObject, error) {
		reply, err := i.gw.Send(ctx, "GET", i.gcp+"/v1/projects/"+project+"/"+kind, map[string]string{}, nil)
		if err != nil {
			return nil, err
		}
		if reply.Status < 200 || reply.Status > 299 {
			return nil, EmulatorError{reply.Status, "Pub/Sub " + kind + " failed: " + obsTake(reply.Body, 200)}
		}
		o, perr := obsParseObject(reply.Body)
		if perr != nil {
			return nil, EmulatorError{502, "Unreadable JSON from emulator"}
		}
		return o, nil
	}
	short := func(s string) string { return s[strings.LastIndex(s, "/")+1:] }

	topicsReply, err := list("topics")
	if err != nil {
		return PubSubDto{}, err
	}
	topics := []TopicDto{}
	for _, t := range obsArr(topicsReply.Get("topics")) {
		name, _ := obsText(obsObj(t).Get("name"))
		topics = append(topics, TopicDto{Name: short(name)})
	}
	subsReply, err := list("subscriptions")
	if err != nil {
		return PubSubDto{}, err
	}
	subs := []SubscriptionDto{}
	for _, s := range obsArr(subsReply.Get("subscriptions")) {
		o := obsObj(s)
		name, _ := obsText(o.Get("name"))
		dto := SubscriptionDto{Name: short(name)}
		if topic, ok := obsText(o.Get("topic")); ok {
			t := short(topic)
			dto.Topic = &t
		}
		subs = append(subs, dto)
	}
	return PubSubDto{Project: project, Topics: topics, Subscriptions: subs}, nil
}

// ---------- plumbing ----------

func inspectAuth(service string) map[string]string {
	return map[string]string{
		"Authorization": "AWS4-HMAC-SHA256 Credential=test/" + inspectPlaceholderDate + "/us-east-1/" + service + "/aws4_request, SignedHeaders=host, Signature=0",
	}
}

// awsCall sends one request to the AWS emulator and turns a non-2xx answer into a short EmulatorError (the XML error
// code when there is one), never the raw body.
func (i *Inspector) awsCall(ctx context.Context, service, method, path string, form *string, extra map[string]string) (HTTPReply, error) {
	headers := inspectAuth(service)
	for k, v := range extra {
		headers[k] = v
	}
	if form != nil {
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	reply, err := i.gw.Send(ctx, method, i.aws+path, headers, form)
	if err != nil {
		return HTTPReply{}, err
	}
	if reply.Status < 200 || reply.Status > 299 {
		detail := obsTake(reply.Body, 120)
		if m := inspectCode.FindStringSubmatch(reply.Body); m != nil {
			detail = m[1]
		}
		return HTTPReply{}, EmulatorError{reply.Status, fmt.Sprintf("%s %d: %s", service, reply.Status, detail)}
	}
	return reply, nil
}

func (i *Inspector) xml(reply HTTPReply, err error) (*obsXMLNode, error) {
	if err != nil {
		return nil, err
	}
	root, perr := obsParseXML(reply.Body)
	if perr != nil {
		return nil, EmulatorError{502, "Unreadable XML from emulator"}
	}
	return root, nil
}

func inspectCheckName(value, what string) error {
	// "." and ".." fit the pattern but would walk out of the bucket path.
	if !inspectName.MatchString(value) || value == "." || value == ".." {
		return InvalidName{"Invalid " + what + " name"}
	}
	return nil
}

// inspectEnc percent-encodes like Java's URLEncoder with spaces as %20, which is what S3 expects.
func inspectEnc(value string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(value) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '*', c == '_':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
