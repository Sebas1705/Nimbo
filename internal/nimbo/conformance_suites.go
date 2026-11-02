package nimbo

import (
	"fmt"
	"time"
)

const (
	fitSQSV = "2012-11-05"
	fitSNSV = "2010-03-31"
	fitIAMV = "2010-05-08"
	fitSTSV = "2011-06-15"
)

// fitSuites is every suite, in the order the matrix shows them: AWS, then Azure, then GCP.
func fitSuites() []fitSuite {
	return []fitSuite{
		{"s3", "aws", "S3", fitS3},
		{"sqs", "aws", "SQS", fitSQS},
		{"sns", "aws", "SNS", fitSNS},
		{"dynamodb", "aws", "DynamoDB", fitDynamo},
		{"ssm", "aws", "SSM Parameter Store", fitSSM},
		{"secretsmanager", "aws", "Secrets Manager", fitSecrets},
		{"kms", "aws", "KMS", fitKMS},
		{"eventbridge", "aws", "EventBridge", fitEventBridge},
		{"kinesis", "aws", "Kinesis", fitKinesis},
		{"logs", "aws", "CloudWatch Logs", fitLogs},
		{"iam-sts", "aws", "IAM and STS", fitIAM},
		{"stepfunctions", "aws", "Step Functions", fitStepFunctions},
		{"lambda", "aws", "Lambda", fitLambda},

		// Azure (Floci AZ)
		{"blob", "azure", "Blob Storage", fitBlob},
		{"queue", "azure", "Queue Storage", fitAzQueue},
		{"table", "azure", "Table Storage", fitAzTable},

		// GCP (Floci GCP)
		{"gcs", "gcp", "Cloud Storage", fitGCS},
		{"secretmanager", "gcp", "Secret Manager", fitGCPSecrets},
		{"pubsub", "gcp", "Pub/Sub", fitPubSub},
	}
}

func fitS3(f *fitCtx) {
	b := "nimbo-fit-" + f.tag
	f.cleanup.Add(func() { f.s3("DELETE", "/"+b+"/hello.txt"); f.s3("DELETE", "/"+b) })
	f.step("create bucket", func() { f.s3("PUT", "/"+b).ok("PUT bucket") })
	f.step("put object", func() { f.s3("PUT", "/"+b+"/hello.txt", "hello nimbo").ok("PUT object") })
	f.step("get returns the same bytes", func() {
		fitNeed(f.s3("GET", "/"+b+"/hello.txt").ok("GET object").Body == "hello nimbo", "body differs")
	})
	f.step("list shows the key", func() {
		fitNeed(fitHas(f.s3("GET", "/"+b+"?list-type=2").ok("LIST").Body, "<Key>hello.txt</Key>"), "key not listed")
	})
	f.step("delete object", func() { f.s3("DELETE", "/"+b+"/hello.txt").ok("DELETE object") })
	f.step("delete bucket", func() { f.s3("DELETE", "/"+b).ok("DELETE bucket") })
}

func fitSQS(f *fitCtx) {
	name := "nimbo-fit-" + f.tag
	path, receipt := "", ""
	q := func(kv ...string) fitReply { return f.query("sqs", path, append(kv, "Version", fitSQSV)...) }
	f.cleanup.Add(func() {
		if path != "" {
			q("Action", "DeleteQueue")
		}
	})
	f.step("create queue", func() {
		reply := f.query("sqs", "/", "Action", "CreateQueue", "QueueName", name, "Version", fitSQSV).ok("CreateQueue")
		path = fitURLPath(fitTagOr(reply.Body, "QueueUrl", "no QueueUrl"))
	})
	f.step("send message", func() {
		_, ok := fitXMLTag(q("Action", "SendMessage", "MessageBody", "hi").ok("SendMessage").Body, "MessageId")
		fitNeed(ok, "no MessageId")
	})
	f.step("receive returns the body", func() {
		body := q("Action", "ReceiveMessage").ok("ReceiveMessage").Body
		got, _ := fitXMLTag(body, "Body")
		fitNeed(got == "hi", "body differs")
		receipt = fitTagOr(body, "ReceiptHandle", "no ReceiptHandle")
	})
	f.step("delete message", func() { q("Action", "DeleteMessage", "ReceiptHandle", receipt).ok("DeleteMessage") })
	f.step("queue is empty afterwards", func() {
		fitNeed(!fitHas(q("Action", "ReceiveMessage").ok("ReceiveMessage").Body, "<Message>"), "message still there")
	})
	f.step("delete queue", func() { q("Action", "DeleteQueue").ok("DeleteQueue"); path = "" })
}

func fitSNS(f *fitCtx) {
	topic := "nimbo-fit-" + f.tag
	queue := "nimbo-fit-q-" + f.tag
	topicArn, queuePath := "", ""
	f.cleanup.Add(func() {
		if topicArn != "" {
			f.query("sns", "/", "Action", "DeleteTopic", "TopicArn", topicArn, "Version", fitSNSV)
		}
	})
	f.cleanup.Add(func() {
		if queuePath != "" {
			f.query("sqs", queuePath, "Action", "DeleteQueue", "Version", fitSQSV)
		}
	})
	f.step("create topic", func() {
		topicArn = fitTagOr(f.query("sns", "/", "Action", "CreateTopic", "Name", topic, "Version", fitSNSV).ok("CreateTopic").Body, "TopicArn", "no TopicArn")
	})
	f.step("list topics contains it", func() {
		fitNeed(fitHas(f.query("sns", "/", "Action", "ListTopics", "Version", fitSNSV).ok("ListTopics").Body, topicArn), "not listed")
	})
	f.step("fan out to an SQS queue", func() {
		created := f.query("sqs", "/", "Action", "CreateQueue", "QueueName", queue, "Version", fitSQSV).ok("CreateQueue")
		queuePath = fitURLPath(fitTagOr(created.Body, "QueueUrl", "no QueueUrl"))
		arn := "arn:aws:sqs:us-east-1:000000000000:" + queue
		f.query("sns", "/", "Action", "Subscribe", "TopicArn", topicArn, "Protocol", "sqs", "Endpoint", arn, "Version", fitSNSV).ok("Subscribe")
		f.query("sns", "/", "Action", "Publish", "TopicArn", topicArn, "Message", "fanout", "Version", fitSNSV).ok("Publish")
		found := false
		for i := 0; i < 4 && !found; i++ {
			found = fitHas(f.query("sqs", queuePath, "Action", "ReceiveMessage", "WaitTimeSeconds", "1", "Version", fitSQSV).ok("ReceiveMessage").Body, "fanout")
		}
		fitNeed(found, "published message never reached the queue")
	})
	f.step("delete topic", func() {
		f.query("sns", "/", "Action", "DeleteTopic", "TopicArn", topicArn, "Version", fitSNSV).ok("DeleteTopic")
		topicArn = ""
	})
}

func fitDynamo(f *fitCtx) {
	t := "nimbo-fit-" + f.tag
	const target = "DynamoDB_20120810"
	d := func(op string, body any) fitReply { return f.jsonCall("dynamodb", target+"."+op, "1.0", body).ok(op) }
	key := fmt.Sprintf(`{"TableName":"%s","Key":{"id":{"S":"1"}}}`, t)
	f.cleanup.Add(func() { f.jsonCall("dynamodb", target+".DeleteTable", "1.0", fitM{"TableName": t}) })
	f.step("create table", func() {
		d("CreateTable", fmt.Sprintf(`{"TableName":"%s","BillingMode":"PAY_PER_REQUEST","KeySchema":[{"AttributeName":"id","KeyType":"HASH"}],"AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}]}`, t))
	})
	f.step("put item", func() { d("PutItem", fmt.Sprintf(`{"TableName":"%s","Item":{"id":{"S":"1"},"n":{"N":"5"}}}`, t)) })
	f.step("get item returns it", func() {
		n, _ := fitStr(fitObj(d("GetItem", key)), "Item", "n", "N")
		fitNeed(n == "5", "item differs")
	})
	f.step("query by key expression", func() {
		reply := d("Query", fmt.Sprintf(`{"TableName":"%s","KeyConditionExpression":"id = :i","ExpressionAttributeValues":{":i":{"S":"1"}}}`, t))
		count, _ := fitStr(fitObj(reply), "Count")
		fitNeed(count == "1", "expected one match")
	})
	f.step("update expression", func() {
		d("UpdateItem", fmt.Sprintf(`{"TableName":"%s","Key":{"id":{"S":"1"}},"UpdateExpression":"SET n = n + :one","ExpressionAttributeValues":{":one":{"N":"1"}}}`, t))
		n, _ := fitStr(fitObj(d("GetItem", key)), "Item", "n", "N")
		fitNeed(n == "6", "counter not incremented")
	})
	f.step("delete item", func() { d("DeleteItem", key) })
	f.step("delete table", func() { d("DeleteTable", fitM{"TableName": t}) })
}

func fitSSM(f *fitCtx) {
	name := "/nimbo-fit/" + f.tag + "/p"
	call := func(op string, body fitM) fitReply { return f.jsonCall("ssm", "AmazonSSM."+op, "1.1", body) }
	f.cleanup.Add(func() { call("DeleteParameter", fitM{"Name": name}) })
	f.step("put parameter", func() { call("PutParameter", fitM{"Name": name, "Value": "v1", "Type": "String"}).ok("PutParameter") })
	f.step("get parameter", func() {
		v, _ := fitStr(fitObj(call("GetParameter", fitM{"Name": name}).ok("GetParameter")), "Parameter", "Value")
		fitNeed(v == "v1", "value differs")
	})
	f.step("get by path", func() {
		reply := call("GetParametersByPath", fitM{"Path": "/nimbo-fit/" + f.tag}).ok("GetParametersByPath")
		fitNeed(fitHas(reply.Body, name), "not returned by path")
	})
	f.step("delete parameter", func() { call("DeleteParameter", fitM{"Name": name}).ok("DeleteParameter") })
}

func fitSecrets(f *fitCtx) {
	name := "nimbo-fit-" + f.tag
	call := func(op string, body fitM) fitReply {
		return f.jsonCall("secretsmanager", "secretsmanager."+op, "1.1", body)
	}
	del := fitM{"SecretId": name, "ForceDeleteWithoutRecovery": true}
	value := func() string {
		v, _ := fitStr(fitObj(call("GetSecretValue", fitM{"SecretId": name}).ok("GetSecretValue")), "SecretString")
		return v
	}
	f.cleanup.Add(func() { call("DeleteSecret", del) })
	f.step("create secret", func() { call("CreateSecret", fitM{"Name": name, "SecretString": "s1"}).ok("CreateSecret") })
	f.step("get secret value", func() { fitNeed(value() == "s1", "value differs") })
	f.step("put a new version", func() {
		call("PutSecretValue", fitM{"SecretId": name, "SecretString": "s2"}).ok("PutSecretValue")
		fitNeed(value() == "s2", "new version not current")
	})
	f.step("delete secret", func() { call("DeleteSecret", del).ok("DeleteSecret") })
}

func fitKMS(f *fitCtx) {
	keyID, cipher := "", ""
	call := func(op string, body any) fitReply { return f.jsonCall("kms", "TrentService."+op, "1.1", body) }
	f.cleanup.Add(func() {
		if keyID != "" {
			call("ScheduleKeyDeletion", fitM{"KeyId": keyID, "PendingWindowInDays": 7})
		}
	})
	f.step("create key", func() {
		keyID = fitStrOr(fitObj(call("CreateKey", nil).ok("CreateKey")), "no KeyId", "KeyMetadata", "KeyId")
	})
	f.step("encrypt", func() {
		cipher = fitStrOr(fitObj(call("Encrypt", fitM{"KeyId": keyID, "Plaintext": fitB64("secret")}).ok("Encrypt")), "no ciphertext", "CiphertextBlob")
	})
	f.step("decrypt round trip", func() {
		p, _ := fitStr(fitObj(call("Decrypt", fitM{"CiphertextBlob": cipher}).ok("Decrypt")), "Plaintext")
		fitNeed(p == fitB64("secret"), "plaintext differs")
	})
	f.step("schedule key deletion", func() {
		call("ScheduleKeyDeletion", fitM{"KeyId": keyID, "PendingWindowInDays": 7}).ok("ScheduleKeyDeletion")
		keyID = ""
	})
}

func fitEventBridge(f *fitCtx) {
	rule := "nimbo-fit-" + f.tag
	call := func(op string, body fitM) fitReply { return f.jsonCall("events", "AWSEvents."+op, "1.1", body) }
	f.cleanup.Add(func() { call("DeleteRule", fitM{"Name": rule}) })
	f.step("put rule", func() { call("PutRule", fitM{"Name": rule, "ScheduleExpression": "rate(5 minutes)"}).ok("PutRule") })
	f.step("list rules contains it", func() {
		fitNeed(fitHas(call("ListRules", fitM{"NamePrefix": "nimbo-fit-"}).ok("ListRules").Body, rule), "not listed")
	})
	f.step("put events", func() {
		body := fitM{"Entries": []any{fitM{"Source": "nimbo.fit", "DetailType": "t", "Detail": "{}"}}}
		n, _ := fitStr(fitObj(call("PutEvents", body).ok("PutEvents")), "FailedEntryCount")
		fitNeed(n == "0", "entry failed")
	})
	f.step("delete rule", func() { call("DeleteRule", fitM{"Name": rule}).ok("DeleteRule") })
}

func fitKinesis(f *fitCtx) {
	stream := "nimbo-fit-" + f.tag
	call := func(op string, body fitM) fitReply { return f.jsonCall("kinesis", "Kinesis_20131202."+op, "1.1", body) }
	del := fitM{"StreamName": stream, "EnforceConsumerDeletion": true}
	f.cleanup.Add(func() { call("DeleteStream", del) })
	f.step("create stream", func() { call("CreateStream", fitM{"StreamName": stream, "ShardCount": 1}).ok("CreateStream") })
	f.step("put record", func() {
		call("PutRecord", fitM{"StreamName": stream, "PartitionKey": "k", "Data": fitB64("hello")}).ok("PutRecord")
	})
	f.step("read it back through a shard iterator", func() {
		first, ok := fitFirst(fitObj(call("ListShards", fitM{"StreamName": stream}).ok("ListShards")), "Shards")
		fitNeed(ok, "no shards")
		shard := fitStrOr(first, "no ShardId", "ShardId")
		iterator := fitStrOr(fitObj(call("GetShardIterator", fitM{"StreamName": stream, "ShardId": shard, "ShardIteratorType": "TRIM_HORIZON"}).ok("GetShardIterator")), "no ShardIterator", "ShardIterator")
		fitNeed(fitHas(call("GetRecords", fitM{"ShardIterator": iterator}).ok("GetRecords").Body, fitB64("hello")), "record not returned")
	})
	f.step("delete stream", func() { call("DeleteStream", del).ok("DeleteStream") })
}

func fitLogs(f *fitCtx) {
	group := "/nimbo-fit/" + f.tag
	call := func(op string, body fitM) fitReply { return f.jsonCall("logs", "Logs_20140328."+op, "1.1", body) }
	f.cleanup.Add(func() { call("DeleteLogGroup", fitM{"logGroupName": group}) })
	f.step("create log group", func() { call("CreateLogGroup", fitM{"logGroupName": group}).ok("CreateLogGroup") })
	f.step("create log stream", func() {
		call("CreateLogStream", fitM{"logGroupName": group, "logStreamName": "s"}).ok("CreateLogStream")
	})
	f.step("put and read events", func() {
		now := time.Now().UnixMilli()
		put := fitM{"logGroupName": group, "logStreamName": "s", "logEvents": []any{fitM{"timestamp": now, "message": "hello logs"}}}
		call("PutLogEvents", put).ok("PutLogEvents")
		read := call("GetLogEvents", fitM{"logGroupName": group, "logStreamName": "s", "startFromHead": true}).ok("GetLogEvents")
		fitNeed(fitHas(read.Body, "hello logs"), "event not returned")
	})
	f.step("delete log group", func() { call("DeleteLogGroup", fitM{"logGroupName": group}).ok("DeleteLogGroup") })
}

func fitIAM(f *fitCtx) {
	role := "nimbo-fit-" + f.tag
	trust := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"sts:AssumeRole"}]}`
	deleteRole := func() fitReply {
		return f.query("iam", "/", "Action", "DeleteRole", "RoleName", role, "Version", fitIAMV)
	}
	f.cleanup.Add(func() { deleteRole() })
	f.step("who am I", func() {
		_, ok := fitXMLTag(f.query("sts", "/", "Action", "GetCallerIdentity", "Version", fitSTSV).ok("GetCallerIdentity").Body, "Account")
		fitNeed(ok, "no account")
	})
	f.step("create role", func() {
		f.query("iam", "/", "Action", "CreateRole", "RoleName", role, "AssumeRolePolicyDocument", trust, "Version", fitIAMV).ok("CreateRole")
	})
	f.step("list roles contains it", func() {
		fitNeed(fitHas(f.query("iam", "/", "Action", "ListRoles", "Version", fitIAMV).ok("ListRoles").Body, role), "not listed")
	})
	f.indep("assume the role", func() {
		arn := "arn:aws:iam::000000000000:role/" + role
		_, ok := fitXMLTag(f.query("sts", "/", "Action", "AssumeRole", "RoleArn", arn, "RoleSessionName", "fit", "Version", fitSTSV).ok("AssumeRole").Body, "AccessKeyId")
		fitNeed(ok, "no credentials")
	})
	f.step("delete role", func() { deleteRole().ok("DeleteRole") })
}

func fitStepFunctions(f *fitCtx) {
	name := "nimbo-fit-" + f.tag
	arn := ""
	call := func(op string, body fitM) fitReply { return f.jsonCall("states", "AWSStepFunctions."+op, "1.0", body) }
	f.cleanup.Add(func() {
		if arn != "" {
			call("DeleteStateMachine", fitM{"stateMachineArn": arn})
		}
	})
	f.step("create state machine", func() {
		definition := `{"StartAt":"Done","States":{"Done":{"Type":"Pass","Result":"ok","End":true}}}`
		body := fitM{"name": name, "definition": definition, "roleArn": "arn:aws:iam::000000000000:role/fit"}
		arn = fitStrOr(fitObj(call("CreateStateMachine", body).ok("CreateStateMachine")), "no ARN", "stateMachineArn")
	})
	f.step("execution succeeds", func() {
		exec := fitStrOr(fitObj(call("StartExecution", fitM{"stateMachineArn": arn}).ok("StartExecution")), "no executionArn", "executionArn")
		status := ""
		for i := 0; i < 5 && status != "SUCCEEDED"; i++ {
			status, _ = fitStr(fitObj(call("DescribeExecution", fitM{"executionArn": exec}).ok("DescribeExecution")), "status")
			if status == "RUNNING" {
				time.Sleep(300 * time.Millisecond)
			}
		}
		fitNeed(status == "SUCCEEDED", fmt.Sprintf("execution ended as '%s'", status))
	})
	f.step("delete state machine", func() {
		call("DeleteStateMachine", fitM{"stateMachineArn": arn}).ok("DeleteStateMachine")
		arn = ""
	})
}

func fitLambda(f *fitCtx) {
	fn := "nimbo-fit-" + f.tag
	f.cleanup.Add(func() { f.lambda("DELETE", "/2015-03-31/functions/"+fn) })
	f.step("list functions", func() { f.lambda("GET", "/2015-03-31/functions").ok("ListFunctions") })
	f.step("create function", func() {
		body := fitM{
			"FunctionName": fn, "Runtime": "nodejs20.x", "Handler": "index.handler",
			"Role": "arn:aws:iam::000000000000:role/fit",
			"Code": fitM{"ZipFile": fitLambdaZip()},
		}
		f.lambda("POST", "/2015-03-31/functions", fitJSON(body)).ok("CreateFunction")
	})
	f.indep("invoke runs the handler (needs the Docker socket)", func() {
		reply := f.lambda("POST", "/2015-03-31/functions/"+fn+"/invocations", "{}").ok("Invoke")
		fitNeed(fitHas(reply.Body, "ok") && !fitHas(reply.Body, "FunctionError"),
			"handler did not return the expected payload: "+fitTake(reply.Body, 80))
	})
	f.step("delete function", func() { f.lambda("DELETE", "/2015-03-31/functions/"+fn).ok("DeleteFunction") })
}

// ---------------- Azure ----------------

func fitBlob(f *fitCtx) {
	c := "fit" + f.tag
	base := "/devstoreaccount1/" + c
	f.cleanup.Add(func() { f.az("DELETE", base+"?restype=container", nil, nil) })
	f.step("create container", func() { f.az("PUT", base+"?restype=container", nil, nil).ok("Create container") })
	f.step("put blob", func() {
		f.az("PUT", base+"/hello.txt", fitSP("hello azure"), map[string]string{"x-ms-blob-type": "BlockBlob"}).ok("Put blob")
	})
	f.step("get returns the same bytes", func() {
		fitNeed(f.az("GET", base+"/hello.txt", nil, nil).ok("Get blob").Body == "hello azure", "body differs")
	})
	f.step("list shows the blob", func() {
		fitNeed(fitHas(f.az("GET", base+"?restype=container&comp=list", nil, nil).ok("List blobs").Body, "hello.txt"), "blob not listed")
	})
	f.step("delete blob", func() { f.az("DELETE", base+"/hello.txt", nil, nil).ok("Delete blob") })
	f.step("delete container", func() { f.az("DELETE", base+"?restype=container", nil, nil).ok("Delete container") })
}

func fitAzQueue(f *fitCtx) {
	q := "fit" + f.tag
	base := "/devstoreaccount1-queue/" + q
	f.cleanup.Add(func() { f.az("DELETE", base, nil, nil) })
	f.step("create queue", func() { f.az("PUT", base, nil, nil).ok("Create queue") })
	f.step("enqueue a plain text message", func() {
		f.az("POST", base+"/messages", fitSP("<QueueMessage><MessageText>hello</MessageText></QueueMessage>"), nil).ok("Put message")
	})
	f.indep("enqueue a base64 message", func() {
		f.az("POST", base+"/messages", fitSP("<QueueMessage><MessageText>"+fitB64("hello")+"</MessageText></QueueMessage>"), nil).ok("Put base64 message")
	})
	f.step("peek shows the message", func() {
		fitNeed(fitHas(f.az("GET", base+"/messages?peekonly=true", nil, nil).ok("Peek").Body, "<MessageText>"), "nothing to peek")
	})
	f.step("dequeue and delete", func() {
		body := f.az("GET", base+"/messages", nil, nil).ok("Get messages").Body
		id := fitTagOr(body, "MessageId", "no message")
		receipt := fitTagOr(body, "PopReceipt", "no pop receipt")
		f.az("DELETE", base+"/messages/"+id+"?popreceipt="+QueryEscape(receipt), nil, nil).ok("Delete message")
	})
	f.step("delete queue", func() { f.az("DELETE", base, nil, nil).ok("Delete queue") })
}

func fitAzTable(f *fitCtx) {
	t := "fit" + f.tag
	h := map[string]string{"Content-Type": "application/json", "Accept": "application/json;odata=nometadata"}
	del := "/devstoreaccount1-table/Tables('" + t + "')"
	f.cleanup.Add(func() { f.az("DELETE", del, nil, h) })
	f.step("create table", func() {
		f.az("POST", "/devstoreaccount1-table/Tables", fitSP(`{"TableName":"`+t+`"}`), h).ok("Create table")
	})
	f.step("insert entity", func() {
		f.az("POST", "/devstoreaccount1-table/"+t, fitSP(`{"PartitionKey":"p","RowKey":"1","name":"x"}`), h).ok("Insert entity")
	})
	f.step("query returns the entity", func() {
		fitNeed(fitHas(f.az("GET", "/devstoreaccount1-table/"+t+"()", nil, h).ok("Query").Body, `"name":"x"`), "entity not returned")
	})
	f.step("delete table", func() { f.az("DELETE", del, nil, h).ok("Delete table") })
}

// ---------------- GCP ----------------

func fitGCS(f *fitCtx) {
	b := "nimbo-fit-" + f.tag
	f.cleanup.Add(func() { f.g("DELETE", "/storage/v1/b/"+b+"/o/hello.txt"); f.g("DELETE", "/storage/v1/b/"+b) })
	f.step("create bucket", func() { f.g("POST", "/storage/v1/b?project=floci-local", `{"name":"`+b+`"}`).ok("Create bucket") })
	f.step("upload object", func() {
		f.gct("POST", "/upload/storage/v1/b/"+b+"/o?uploadType=media&name=hello.txt", "text/plain", "hello gcs").ok("Upload")
	})
	f.step("list shows the object", func() {
		fitNeed(fitHas(f.g("GET", "/storage/v1/b/"+b+"/o").ok("List objects").Body, "hello.txt"), "not listed")
	})
	f.step("download returns the same bytes", func() {
		fitNeed(f.g("GET", "/storage/v1/b/"+b+"/o/hello.txt?alt=media").ok("Download").Body == "hello gcs", "body differs")
	})
	f.step("delete object", func() { f.g("DELETE", "/storage/v1/b/"+b+"/o/hello.txt").ok("Delete object") })
	f.step("delete bucket", func() { f.g("DELETE", "/storage/v1/b/"+b).ok("Delete bucket") })
}

func fitGCPSecrets(f *fitCtx) {
	s := "nimbo-fit-" + f.tag
	base := "/v1/projects/floci-local/secrets"
	f.cleanup.Add(func() { f.g("DELETE", base+"/"+s) })
	f.step("create secret", func() { f.g("POST", base+"?secretId="+s, `{"replication":{"automatic":{}}}`).ok("Create secret") })
	f.step("add version", func() {
		f.g("POST", base+"/"+s+":addVersion", `{"payload":{"data":"`+fitB64("shh")+`"}}`).ok("Add version")
	})
	f.step("access latest", func() {
		data, _ := fitStr(fitObj(f.g("GET", base+"/"+s+"/versions/latest:access").ok("Access")), "payload", "data")
		fitNeed(data == fitB64("shh"), "payload differs")
	})
	f.step("delete secret", func() { f.g("DELETE", base+"/"+s).ok("Delete secret") })
}

func fitPubSub(f *fitCtx) {
	topic := "nimbo-fit-" + f.tag
	sub := "nimbo-fit-sub-" + f.tag
	base := "/v1/projects/floci-local"
	ackID := ""
	f.cleanup.Add(func() { f.g("DELETE", base+"/subscriptions/"+sub); f.g("DELETE", base+"/topics/"+topic) })
	f.step("create topic", func() { f.g("PUT", base+"/topics/"+topic).ok("Create topic") })
	f.step("create subscription", func() {
		f.g("PUT", base+"/subscriptions/"+sub, `{"topic":"projects/floci-local/topics/`+topic+`"}`).ok("Create subscription")
	})
	f.step("publish", func() {
		reply := f.g("POST", base+"/topics/"+topic+":publish", `{"messages":[{"data":"`+fitB64("hi")+`"}]}`).ok("Publish")
		fitNeed(fitHas(reply.Body, "messageIds"), "no messageIds")
	})
	f.step("pull returns the message", func() {
		reply := fitObj(f.g("POST", base+"/subscriptions/"+sub+":pull", `{"maxMessages":5}`).ok("Pull"))
		first, ok := fitFirst(reply, "receivedMessages")
		if !ok {
			panic(StepFailed{"nothing pulled"})
		}
		data, _ := fitStr(first, "message", "data")
		fitNeed(data == fitB64("hi"), "payload differs")
		ackID = fitStrOr(first, "no ackId", "ackId")
	})
	f.step("acknowledge", func() {
		f.g("POST", base+"/subscriptions/"+sub+":acknowledge", `{"ackIds":["`+ackID+`"]}`).ok("Acknowledge")
	})
	f.step("delete subscription and topic", func() {
		f.g("DELETE", base+"/subscriptions/"+sub).ok("Delete subscription")
		f.g("DELETE", base+"/topics/"+topic).ok("Delete topic")
	})
}
