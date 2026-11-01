package es.sebas1705.nimbo

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import java.io.ByteArrayOutputStream
import java.net.URLEncoder
import java.util.Base64
import java.util.UUID
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream

class StepFailed(message: String) : Exception(message)

@Serializable data class FitStepDto(val name: String, val status: String, val detail: String? = null, val ms: Long = 0)
@Serializable data class SuiteResultDto(val id: String, val cloud: String, val name: String, val status: String, val steps: List<FitStepDto>, val ms: Long)
@Serializable data class SuiteRefDto(val cloud: String, val id: String, val name: String)
@Serializable data class FidelityStateDto(val running: Boolean, val updatedAt: Long?, val catalog: List<SuiteRefDto>, val results: List<SuiteResultDto>, val summary: Map<String, Int>)
@Serializable data class FidelityReportDto(val startedAt: Long, val durationMs: Long, val suites: List<SuiteResultDto>, val summary: Map<String, Int>)

/** Runs one suite: records steps, and skips the ones that depend on an earlier failure. */
class Scenario {
    val steps = mutableListOf<FitStepDto>()
    private var failed = false

    suspend fun step(name: String, independent: Boolean = false, block: suspend () -> Unit) {
        if (failed && !independent) {
            steps += FitStepDto(name, "skipped", "an earlier step failed")
            return
        }
        val started = System.nanoTime()
        fun elapsed() = (System.nanoTime() - started) / 1_000_000
        try {
            block()
            steps += FitStepDto(name, "pass", ms = elapsed())
        } catch (e: EmulatorUnreachable) {
            throw e
        } catch (e: Exception) {
            if (!independent) failed = true
            steps += FitStepDto(name, "fail", (e.message ?: e::class.simpleName).orEmpty().take(220), elapsed())
        }
    }
}

class Cleanups {
    private val actions = mutableListOf<suspend () -> Unit>()
    fun add(action: suspend () -> Unit) { actions += action }
    suspend fun run() = actions.asReversed().forEach { runCatching { it() } }
}

private class Suite(val id: String, val cloud: String, val name: String, val run: suspend Ctx.() -> Unit)

private class Ctx(val s: Scenario, val cleanup: Cleanups, val tag: String) {
    suspend fun step(name: String, independent: Boolean = false, block: suspend () -> Unit) = s.step(name, independent, block)
}

private fun HttpReply.ok(what: String, vararg codes: Int): HttpReply {
    val accepted = if (codes.isEmpty()) 200..299 else codes.toList()
    if (status !in accepted) throw StepFailed("$what -> HTTP $status: ${body.replace(Regex("\\s+"), " ").take(120)}")
    return this
}

private fun need(condition: Boolean, message: String) { if (!condition) throw StepFailed(message) }
private fun xmlTag(xml: String, name: String) = Regex("<$name>([^<]*)</$name>").find(xml)?.groupValues?.get(1)
private fun enc(s: String) = URLEncoder.encode(s, Charsets.UTF_8)
private fun b64(s: String) = Base64.getEncoder().encodeToString(s.toByteArray())

/**
 * Exercises real operations on each emulator and reports what works, what only partly works and what is
 * missing. It is the honest answer to "can I trust this emulator for X?", and it also surfaces emulator bugs.
 */
class Conformance(
    private val http: HttpGateway,
    private val aws: String = "http://localhost:4566",
    private val azure: String = "http://localhost:4577",
    private val gcp: String = "http://localhost:4588",
) {
    private val json = Json { ignoreUnknownKeys = true }

    val catalog: List<SuiteRefDto> get() = suites.map { SuiteRefDto(it.cloud, it.id, it.name) }

    suspend fun run(cloud: String? = null, suite: String? = null): FidelityReportDto {
        val started = System.currentTimeMillis()
        val results = suites
            .filter { (cloud == null || it.cloud == cloud) && (suite == null || it.id == suite) }
            .map { runSuite(it) }
        val summary = results.groupingBy { it.status }.eachCount()
        return FidelityReportDto(started, System.currentTimeMillis() - started, results, summary)
    }

    private suspend fun runSuite(suite: Suite): SuiteResultDto {
        val scenario = Scenario()
        val cleanup = Cleanups()
        val started = System.nanoTime()
        var offline = false
        try {
            suite.run(Ctx(scenario, cleanup, UUID.randomUUID().toString().take(8)))
        } catch (e: EmulatorUnreachable) {
            offline = true
        } finally {
            if (!offline) cleanup.run()
        }
        val steps = scenario.steps
        val passed = steps.count { it.status == "pass" }
        val status = when {
            offline -> "offline"
            steps.isEmpty() || passed == 0 -> "missing"
            passed == steps.size -> "full"
            else -> "partial"
        }
        return SuiteResultDto(suite.id, suite.cloud, suite.name, status, steps, (System.nanoTime() - started) / 1_000_000)
    }

    // ---------- wire helpers ----------

    private fun awsAuth(service: String) =
        mapOf("Authorization" to "AWS4-HMAC-SHA256 Credential=test/20260101/us-east-1/$service/aws4_request, SignedHeaders=host, Signature=0")

    private suspend fun s3(method: String, path: String, body: String? = null) =
        http.send(method, aws + path, awsAuth("s3"), body)

    private suspend fun query(service: String, params: Map<String, String>, path: String = "/"): HttpReply {
        val form = params.entries.joinToString("&") { "${enc(it.key)}=${enc(it.value)}" }
        return http.send("POST", aws + path, awsAuth(service) + ("Content-Type" to "application/x-www-form-urlencoded"), form)
    }

    private suspend fun jsonCall(service: String, target: String, version: String, body: JsonObject = JsonObject(emptyMap())): HttpReply =
        http.send("POST", "$aws/", awsAuth(service) + mapOf("X-Amz-Target" to target, "Content-Type" to "application/x-amz-json-$version"), body.toString())

    private fun JsonObject.str(vararg path: String): String? {
        var current: kotlinx.serialization.json.JsonElement = this
        for (key in path) current = (current as? JsonObject)?.get(key) ?: return null
        return runCatching { current.jsonPrimitive.content }.getOrNull()
    }

    private fun obj(reply: HttpReply): JsonObject = json.parseToJsonElement(reply.body.ifBlank { "{}" }).jsonObject

    private fun azureHeaders(extra: Map<String, String> = emptyMap()) = mapOf(
        "x-ms-version" to "2021-08-06",
        "x-ms-date" to java.time.ZonedDateTime.now(java.time.ZoneOffset.UTC).format(java.time.format.DateTimeFormatter.RFC_1123_DATE_TIME),
        "Authorization" to "SharedKey devstoreaccount1:AAAA",
    ) + extra

    private suspend fun az(method: String, path: String, body: String? = null, extra: Map<String, String> = emptyMap()) =
        http.send(method, azure + path, azureHeaders(extra), body)

    private suspend fun g(method: String, path: String, body: String? = null, contentType: String = "application/json") =
        http.send(method, gcp + path, mapOf("Content-Type" to contentType), body)

    private fun lambdaZip(): String {
        val out = ByteArrayOutputStream()
        ZipOutputStream(out).use {
            it.putNextEntry(ZipEntry("index.js"))
            it.write("exports.handler = async () => ({ ok: true });".toByteArray())
            it.closeEntry()
        }
        return Base64.getEncoder().encodeToString(out.toByteArray())
    }

    // ---------- suites ----------

    private val suites: List<Suite> = listOf(
        Suite("s3", "aws", "S3") {
            val b = "nimbo-fit-$tag"
            cleanup.add { s3("DELETE", "/$b/hello.txt"); s3("DELETE", "/$b") }
            step("create bucket") { s3("PUT", "/$b").ok("PUT bucket") }
            step("put object") { s3("PUT", "/$b/hello.txt", "hello nimbo").ok("PUT object") }
            step("get returns the same bytes") { need(s3("GET", "/$b/hello.txt").ok("GET object").body == "hello nimbo", "body differs") }
            step("list shows the key") { need("<Key>hello.txt</Key>" in s3("GET", "/$b?list-type=2").ok("LIST").body, "key not listed") }
            step("delete object") { s3("DELETE", "/$b/hello.txt").ok("DELETE object") }
            step("delete bucket") { s3("DELETE", "/$b").ok("DELETE bucket") }
        },

        Suite("sqs", "aws", "SQS") {
            val name = "nimbo-fit-$tag"
            var path = ""
            var receipt = ""
            cleanup.add { if (path.isNotEmpty()) query("sqs", mapOf("Action" to "DeleteQueue", "Version" to "2012-11-05"), path) }
            step("create queue") {
                val reply = query("sqs", mapOf("Action" to "CreateQueue", "QueueName" to name, "Version" to "2012-11-05")).ok("CreateQueue")
                path = java.net.URI.create(xmlTag(reply.body, "QueueUrl") ?: throw StepFailed("no QueueUrl")).path
            }
            step("send message") { need(xmlTag(query("sqs", mapOf("Action" to "SendMessage", "MessageBody" to "hi", "Version" to "2012-11-05"), path).ok("SendMessage").body, "MessageId") != null, "no MessageId") }
            step("receive returns the body") {
                val body = query("sqs", mapOf("Action" to "ReceiveMessage", "Version" to "2012-11-05"), path).ok("ReceiveMessage").body
                need(xmlTag(body, "Body") == "hi", "body differs")
                receipt = xmlTag(body, "ReceiptHandle") ?: throw StepFailed("no ReceiptHandle")
            }
            step("delete message") { query("sqs", mapOf("Action" to "DeleteMessage", "ReceiptHandle" to receipt, "Version" to "2012-11-05"), path).ok("DeleteMessage") }
            step("queue is empty afterwards") {
                need("<Message>" !in query("sqs", mapOf("Action" to "ReceiveMessage", "Version" to "2012-11-05"), path).ok("ReceiveMessage").body, "message still there")
            }
            step("delete queue") { query("sqs", mapOf("Action" to "DeleteQueue", "Version" to "2012-11-05"), path).ok("DeleteQueue"); path = "" }
        },

        Suite("sns", "aws", "SNS") {
            val topic = "nimbo-fit-$tag"
            val queue = "nimbo-fit-q-$tag"
            var topicArn = ""
            var queuePath = ""
            cleanup.add { if (topicArn.isNotEmpty()) query("sns", mapOf("Action" to "DeleteTopic", "TopicArn" to topicArn, "Version" to "2010-03-31")) }
            cleanup.add { if (queuePath.isNotEmpty()) query("sqs", mapOf("Action" to "DeleteQueue", "Version" to "2012-11-05"), queuePath) }
            step("create topic") { topicArn = xmlTag(query("sns", mapOf("Action" to "CreateTopic", "Name" to topic, "Version" to "2010-03-31")).ok("CreateTopic").body, "TopicArn") ?: throw StepFailed("no TopicArn") }
            step("list topics contains it") { need(topicArn in query("sns", mapOf("Action" to "ListTopics", "Version" to "2010-03-31")).ok("ListTopics").body, "not listed") }
            step("fan out to an SQS queue") {
                val created = query("sqs", mapOf("Action" to "CreateQueue", "QueueName" to queue, "Version" to "2012-11-05")).ok("CreateQueue")
                queuePath = java.net.URI.create(xmlTag(created.body, "QueueUrl") ?: throw StepFailed("no QueueUrl")).path
                val arn = "arn:aws:sqs:us-east-1:000000000000:$queue"
                query("sns", mapOf("Action" to "Subscribe", "TopicArn" to topicArn, "Protocol" to "sqs", "Endpoint" to arn, "Version" to "2010-03-31")).ok("Subscribe")
                query("sns", mapOf("Action" to "Publish", "TopicArn" to topicArn, "Message" to "fanout", "Version" to "2010-03-31")).ok("Publish")
                var found = false
                repeat(4) {
                    if (!found) found = "fanout" in query("sqs", mapOf("Action" to "ReceiveMessage", "WaitTimeSeconds" to "1", "Version" to "2012-11-05"), queuePath).ok("ReceiveMessage").body
                }
                need(found, "published message never reached the queue")
            }
            step("delete topic") { query("sns", mapOf("Action" to "DeleteTopic", "TopicArn" to topicArn, "Version" to "2010-03-31")).ok("DeleteTopic"); topicArn = "" }
        },

        Suite("dynamodb", "aws", "DynamoDB") {
            val t = "nimbo-fit-$tag"
            val target = "DynamoDB_20120810"
            suspend fun d(op: String, body: JsonObject) = jsonCall("dynamodb", "$target.$op", "1.0", body).ok(op)
            cleanup.add { jsonCall("dynamodb", "$target.DeleteTable", "1.0", buildJsonObject { put("TableName", t) }) }
            step("create table") {
                d("CreateTable", json.parseToJsonElement("""{"TableName":"$t","BillingMode":"PAY_PER_REQUEST","KeySchema":[{"AttributeName":"id","KeyType":"HASH"}],"AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}]}""").jsonObject)
            }
            step("put item") { d("PutItem", json.parseToJsonElement("""{"TableName":"$t","Item":{"id":{"S":"1"},"n":{"N":"5"}}}""").jsonObject) }
            step("get item returns it") { need(d("GetItem", json.parseToJsonElement("""{"TableName":"$t","Key":{"id":{"S":"1"}}}""").jsonObject).let { obj(it).str("Item", "n", "N") } == "5", "item differs") }
            step("query by key expression") {
                val reply = d("Query", json.parseToJsonElement("""{"TableName":"$t","KeyConditionExpression":"id = :i","ExpressionAttributeValues":{":i":{"S":"1"}}}""").jsonObject)
                need(obj(reply).str("Count") == "1", "expected one match")
            }
            step("update expression") {
                d("UpdateItem", json.parseToJsonElement("""{"TableName":"$t","Key":{"id":{"S":"1"}},"UpdateExpression":"SET n = n + :one","ExpressionAttributeValues":{":one":{"N":"1"}}}""").jsonObject)
                need(obj(d("GetItem", json.parseToJsonElement("""{"TableName":"$t","Key":{"id":{"S":"1"}}}""").jsonObject)).str("Item", "n", "N") == "6", "counter not incremented")
            }
            step("delete item") { d("DeleteItem", json.parseToJsonElement("""{"TableName":"$t","Key":{"id":{"S":"1"}}}""").jsonObject) }
            step("delete table") { d("DeleteTable", buildJsonObject { put("TableName", t) }) }
        },

        Suite("ssm", "aws", "SSM Parameter Store") {
            val name = "/nimbo-fit/$tag/p"
            cleanup.add { jsonCall("ssm", "AmazonSSM.DeleteParameter", "1.1", buildJsonObject { put("Name", name) }) }
            step("put parameter") { jsonCall("ssm", "AmazonSSM.PutParameter", "1.1", buildJsonObject { put("Name", name); put("Value", "v1"); put("Type", "String") }).ok("PutParameter") }
            step("get parameter") { need(obj(jsonCall("ssm", "AmazonSSM.GetParameter", "1.1", buildJsonObject { put("Name", name) }).ok("GetParameter")).str("Parameter", "Value") == "v1", "value differs") }
            step("get by path") {
                val reply = jsonCall("ssm", "AmazonSSM.GetParametersByPath", "1.1", buildJsonObject { put("Path", "/nimbo-fit/$tag") }).ok("GetParametersByPath")
                need(name in reply.body, "not returned by path")
            }
            step("delete parameter") { jsonCall("ssm", "AmazonSSM.DeleteParameter", "1.1", buildJsonObject { put("Name", name) }).ok("DeleteParameter") }
        },

        Suite("secretsmanager", "aws", "Secrets Manager") {
            val name = "nimbo-fit-$tag"
            cleanup.add { jsonCall("secretsmanager", "secretsmanager.DeleteSecret", "1.1", buildJsonObject { put("SecretId", name); put("ForceDeleteWithoutRecovery", true) }) }
            step("create secret") { jsonCall("secretsmanager", "secretsmanager.CreateSecret", "1.1", buildJsonObject { put("Name", name); put("SecretString", "s1") }).ok("CreateSecret") }
            step("get secret value") { need(obj(jsonCall("secretsmanager", "secretsmanager.GetSecretValue", "1.1", buildJsonObject { put("SecretId", name) }).ok("GetSecretValue")).str("SecretString") == "s1", "value differs") }
            step("put a new version") {
                jsonCall("secretsmanager", "secretsmanager.PutSecretValue", "1.1", buildJsonObject { put("SecretId", name); put("SecretString", "s2") }).ok("PutSecretValue")
                need(obj(jsonCall("secretsmanager", "secretsmanager.GetSecretValue", "1.1", buildJsonObject { put("SecretId", name) }).ok("GetSecretValue")).str("SecretString") == "s2", "new version not current")
            }
            step("delete secret") { jsonCall("secretsmanager", "secretsmanager.DeleteSecret", "1.1", buildJsonObject { put("SecretId", name); put("ForceDeleteWithoutRecovery", true) }).ok("DeleteSecret") }
        },

        Suite("kms", "aws", "KMS") {
            var keyId = ""
            cleanup.add { if (keyId.isNotEmpty()) jsonCall("kms", "TrentService.ScheduleKeyDeletion", "1.1", buildJsonObject { put("KeyId", keyId); put("PendingWindowInDays", 7) }) }
            var cipher = ""
            step("create key") { keyId = obj(jsonCall("kms", "TrentService.CreateKey", "1.1").ok("CreateKey")).str("KeyMetadata", "KeyId") ?: throw StepFailed("no KeyId") }
            step("encrypt") { cipher = obj(jsonCall("kms", "TrentService.Encrypt", "1.1", buildJsonObject { put("KeyId", keyId); put("Plaintext", b64("secret")) }).ok("Encrypt")).str("CiphertextBlob") ?: throw StepFailed("no ciphertext") }
            step("decrypt round trip") { need(obj(jsonCall("kms", "TrentService.Decrypt", "1.1", buildJsonObject { put("CiphertextBlob", cipher) }).ok("Decrypt")).str("Plaintext") == b64("secret"), "plaintext differs") }
            step("schedule key deletion") { jsonCall("kms", "TrentService.ScheduleKeyDeletion", "1.1", buildJsonObject { put("KeyId", keyId); put("PendingWindowInDays", 7) }).ok("ScheduleKeyDeletion"); keyId = "" }
        },

        Suite("eventbridge", "aws", "EventBridge") {
            val rule = "nimbo-fit-$tag"
            cleanup.add { jsonCall("events", "AWSEvents.DeleteRule", "1.1", buildJsonObject { put("Name", rule) }) }
            step("put rule") { jsonCall("events", "AWSEvents.PutRule", "1.1", buildJsonObject { put("Name", rule); put("ScheduleExpression", "rate(5 minutes)") }).ok("PutRule") }
            step("list rules contains it") { need(rule in jsonCall("events", "AWSEvents.ListRules", "1.1", buildJsonObject { put("NamePrefix", "nimbo-fit-") }).ok("ListRules").body, "not listed") }
            step("put events") {
                val body = buildJsonObject {
                    put("Entries", buildJsonArray { add(buildJsonObject { put("Source", "nimbo.fit"); put("DetailType", "t"); put("Detail", "{}") }) })
                }
                need(obj(jsonCall("events", "AWSEvents.PutEvents", "1.1", body).ok("PutEvents")).str("FailedEntryCount") == "0", "entry failed")
            }
            step("delete rule") { jsonCall("events", "AWSEvents.DeleteRule", "1.1", buildJsonObject { put("Name", rule) }).ok("DeleteRule") }
        },

        Suite("kinesis", "aws", "Kinesis") {
            val stream = "nimbo-fit-$tag"
            cleanup.add { jsonCall("kinesis", "Kinesis_20131202.DeleteStream", "1.1", buildJsonObject { put("StreamName", stream); put("EnforceConsumerDeletion", true) }) }
            step("create stream") { jsonCall("kinesis", "Kinesis_20131202.CreateStream", "1.1", buildJsonObject { put("StreamName", stream); put("ShardCount", 1) }).ok("CreateStream") }
            step("put record") { jsonCall("kinesis", "Kinesis_20131202.PutRecord", "1.1", buildJsonObject { put("StreamName", stream); put("PartitionKey", "k"); put("Data", b64("hello")) }).ok("PutRecord") }
            step("read it back through a shard iterator") {
                val shard = obj(jsonCall("kinesis", "Kinesis_20131202.ListShards", "1.1", buildJsonObject { put("StreamName", stream) }).ok("ListShards"))["Shards"]!!.jsonArray.first().jsonObject.str("ShardId")!!
                val iterator = obj(jsonCall("kinesis", "Kinesis_20131202.GetShardIterator", "1.1", buildJsonObject { put("StreamName", stream); put("ShardId", shard); put("ShardIteratorType", "TRIM_HORIZON") }).ok("GetShardIterator")).str("ShardIterator")!!
                need(b64("hello") in jsonCall("kinesis", "Kinesis_20131202.GetRecords", "1.1", buildJsonObject { put("ShardIterator", iterator) }).ok("GetRecords").body, "record not returned")
            }
            step("delete stream") { jsonCall("kinesis", "Kinesis_20131202.DeleteStream", "1.1", buildJsonObject { put("StreamName", stream); put("EnforceConsumerDeletion", true) }).ok("DeleteStream") }
        },

        Suite("logs", "aws", "CloudWatch Logs") {
            val group = "/nimbo-fit/$tag"
            cleanup.add { jsonCall("logs", "Logs_20140328.DeleteLogGroup", "1.1", buildJsonObject { put("logGroupName", group) }) }
            step("create log group") { jsonCall("logs", "Logs_20140328.CreateLogGroup", "1.1", buildJsonObject { put("logGroupName", group) }).ok("CreateLogGroup") }
            step("create log stream") { jsonCall("logs", "Logs_20140328.CreateLogStream", "1.1", buildJsonObject { put("logGroupName", group); put("logStreamName", "s") }).ok("CreateLogStream") }
            step("put and read events") {
                val now = System.currentTimeMillis()
                val put = buildJsonObject {
                    put("logGroupName", group); put("logStreamName", "s")
                    put("logEvents", buildJsonArray { add(buildJsonObject { put("timestamp", now); put("message", "hello logs") }) })
                }
                jsonCall("logs", "Logs_20140328.PutLogEvents", "1.1", put).ok("PutLogEvents")
                val read = jsonCall("logs", "Logs_20140328.GetLogEvents", "1.1", buildJsonObject { put("logGroupName", group); put("logStreamName", "s"); put("startFromHead", true) }).ok("GetLogEvents")
                need("hello logs" in read.body, "event not returned")
            }
            step("delete log group") { jsonCall("logs", "Logs_20140328.DeleteLogGroup", "1.1", buildJsonObject { put("logGroupName", group) }).ok("DeleteLogGroup") }
        },

        Suite("iam-sts", "aws", "IAM and STS") {
            val role = "nimbo-fit-$tag"
            val trust = """{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"sts:AssumeRole"}]}"""
            cleanup.add { query("iam", mapOf("Action" to "DeleteRole", "RoleName" to role, "Version" to "2010-05-08")) }
            step("who am I") { need(xmlTag(query("sts", mapOf("Action" to "GetCallerIdentity", "Version" to "2011-06-15")).ok("GetCallerIdentity").body, "Account") != null, "no account") }
            step("create role") { query("iam", mapOf("Action" to "CreateRole", "RoleName" to role, "AssumeRolePolicyDocument" to trust, "Version" to "2010-05-08")).ok("CreateRole") }
            step("list roles contains it") { need(role in query("iam", mapOf("Action" to "ListRoles", "Version" to "2010-05-08")).ok("ListRoles").body, "not listed") }
            step("assume the role", independent = true) {
                val arn = "arn:aws:iam::000000000000:role/$role"
                need(xmlTag(query("sts", mapOf("Action" to "AssumeRole", "RoleArn" to arn, "RoleSessionName" to "fit", "Version" to "2011-06-15")).ok("AssumeRole").body, "AccessKeyId") != null, "no credentials")
            }
            step("delete role") { query("iam", mapOf("Action" to "DeleteRole", "RoleName" to role, "Version" to "2010-05-08")).ok("DeleteRole") }
        },

        Suite("stepfunctions", "aws", "Step Functions") {
            val name = "nimbo-fit-$tag"
            var arn = ""
            cleanup.add { if (arn.isNotEmpty()) jsonCall("states", "AWSStepFunctions.DeleteStateMachine", "1.0", buildJsonObject { put("stateMachineArn", arn) }) }
            step("create state machine") {
                val definition = """{"StartAt":"Done","States":{"Done":{"Type":"Pass","Result":"ok","End":true}}}"""
                val body = buildJsonObject { put("name", name); put("definition", definition); put("roleArn", "arn:aws:iam::000000000000:role/fit") }
                arn = obj(jsonCall("states", "AWSStepFunctions.CreateStateMachine", "1.0", body).ok("CreateStateMachine")).str("stateMachineArn") ?: throw StepFailed("no ARN")
            }
            step("execution succeeds") {
                val exec = obj(jsonCall("states", "AWSStepFunctions.StartExecution", "1.0", buildJsonObject { put("stateMachineArn", arn) }).ok("StartExecution")).str("executionArn") ?: throw StepFailed("no executionArn")
                var status = ""
                repeat(5) {
                    if (status != "SUCCEEDED") {
                        status = obj(jsonCall("states", "AWSStepFunctions.DescribeExecution", "1.0", buildJsonObject { put("executionArn", exec) }).ok("DescribeExecution")).str("status").orEmpty()
                        if (status == "RUNNING") Thread.sleep(300)
                    }
                }
                need(status == "SUCCEEDED", "execution ended as '$status'")
            }
            step("delete state machine") { jsonCall("states", "AWSStepFunctions.DeleteStateMachine", "1.0", buildJsonObject { put("stateMachineArn", arn) }).ok("DeleteStateMachine"); arn = "" }
        },

        Suite("lambda", "aws", "Lambda") {
            val fn = "nimbo-fit-$tag"
            fun lambda(method: String, path: String, body: String? = null) = suspend { http.send(method, aws + path, awsAuth("lambda") + ("Content-Type" to "application/json"), body) }
            cleanup.add { lambda("DELETE", "/2015-03-31/functions/$fn")() }
            step("list functions") { lambda("GET", "/2015-03-31/functions")().ok("ListFunctions") }
            step("create function") {
                val body = buildJsonObject {
                    put("FunctionName", fn); put("Runtime", "nodejs20.x"); put("Handler", "index.handler")
                    put("Role", "arn:aws:iam::000000000000:role/fit")
                    put("Code", buildJsonObject { put("ZipFile", lambdaZip()) })
                }
                lambda("POST", "/2015-03-31/functions", body.toString())().ok("CreateFunction")
            }
            step("invoke runs the handler (needs the Docker socket)", independent = true) {
                val reply = lambda("POST", "/2015-03-31/functions/$fn/invocations", "{}")().ok("Invoke")
                need("ok" in reply.body && "FunctionError" !in reply.body, "handler did not return the expected payload: ${reply.body.take(80)}")
            }
            step("delete function") { lambda("DELETE", "/2015-03-31/functions/$fn")().ok("DeleteFunction") }
        },

        // ---------------- Azure (Floci AZ) ----------------

        Suite("blob", "azure", "Blob Storage") {
            val c = "fit$tag"
            cleanup.add { az("DELETE", "/devstoreaccount1/$c?restype=container") }
            step("create container") { az("PUT", "/devstoreaccount1/$c?restype=container").ok("Create container") }
            step("put blob") { az("PUT", "/devstoreaccount1/$c/hello.txt", "hello azure", mapOf("x-ms-blob-type" to "BlockBlob")).ok("Put blob") }
            step("get returns the same bytes") { need(az("GET", "/devstoreaccount1/$c/hello.txt").ok("Get blob").body == "hello azure", "body differs") }
            step("list shows the blob") { need("hello.txt" in az("GET", "/devstoreaccount1/$c?restype=container&comp=list").ok("List blobs").body, "blob not listed") }
            step("delete blob") { az("DELETE", "/devstoreaccount1/$c/hello.txt").ok("Delete blob") }
            step("delete container") { az("DELETE", "/devstoreaccount1/$c?restype=container").ok("Delete container") }
        },

        Suite("queue", "azure", "Queue Storage") {
            val q = "fit$tag"
            cleanup.add { az("DELETE", "/devstoreaccount1-queue/$q") }
            step("create queue") { az("PUT", "/devstoreaccount1-queue/$q").ok("Create queue") }
            step("enqueue a plain text message") { az("POST", "/devstoreaccount1-queue/$q/messages", "<QueueMessage><MessageText>hello</MessageText></QueueMessage>").ok("Put message") }
            step("enqueue a base64 message", independent = true) { az("POST", "/devstoreaccount1-queue/$q/messages", "<QueueMessage><MessageText>${b64("hello")}</MessageText></QueueMessage>").ok("Put base64 message") }
            step("peek shows the message") { need("<MessageText>" in az("GET", "/devstoreaccount1-queue/$q/messages?peekonly=true").ok("Peek").body, "nothing to peek") }
            step("dequeue and delete") {
                val body = az("GET", "/devstoreaccount1-queue/$q/messages").ok("Get messages").body
                val id = xmlTag(body, "MessageId") ?: throw StepFailed("no message")
                val receipt = xmlTag(body, "PopReceipt") ?: throw StepFailed("no pop receipt")
                az("DELETE", "/devstoreaccount1-queue/$q/messages/$id?popreceipt=${enc(receipt)}").ok("Delete message")
            }
            step("delete queue") { az("DELETE", "/devstoreaccount1-queue/$q").ok("Delete queue") }
        },

        Suite("table", "azure", "Table Storage") {
            val t = "fit$tag"
            val h = mapOf("Content-Type" to "application/json", "Accept" to "application/json;odata=nometadata")
            cleanup.add { az("DELETE", "/devstoreaccount1-table/Tables('$t')", null, h) }
            step("create table") { az("POST", "/devstoreaccount1-table/Tables", """{"TableName":"$t"}""", h).ok("Create table") }
            step("insert entity") { az("POST", "/devstoreaccount1-table/$t", """{"PartitionKey":"p","RowKey":"1","name":"x"}""", h).ok("Insert entity") }
            step("query returns the entity") { need("\"name\":\"x\"" in az("GET", "/devstoreaccount1-table/$t()", null, h).ok("Query").body, "entity not returned") }
            step("delete table") { az("DELETE", "/devstoreaccount1-table/Tables('$t')", null, h).ok("Delete table") }
        },

        // ---------------- GCP (Floci GCP) ----------------

        Suite("gcs", "gcp", "Cloud Storage") {
            val b = "nimbo-fit-$tag"
            cleanup.add { g("DELETE", "/storage/v1/b/$b/o/hello.txt"); g("DELETE", "/storage/v1/b/$b") }
            step("create bucket") { g("POST", "/storage/v1/b?project=floci-local", """{"name":"$b"}""").ok("Create bucket") }
            step("upload object") { g("POST", "/upload/storage/v1/b/$b/o?uploadType=media&name=hello.txt", "hello gcs", "text/plain").ok("Upload") }
            step("list shows the object") { need("hello.txt" in g("GET", "/storage/v1/b/$b/o").ok("List objects").body, "not listed") }
            step("download returns the same bytes") { need(g("GET", "/storage/v1/b/$b/o/hello.txt?alt=media").ok("Download").body == "hello gcs", "body differs") }
            step("delete object") { g("DELETE", "/storage/v1/b/$b/o/hello.txt").ok("Delete object") }
            step("delete bucket") { g("DELETE", "/storage/v1/b/$b").ok("Delete bucket") }
        },

        Suite("secretmanager", "gcp", "Secret Manager") {
            val s = "nimbo-fit-$tag"
            cleanup.add { g("DELETE", "/v1/projects/floci-local/secrets/$s") }
            step("create secret") { g("POST", "/v1/projects/floci-local/secrets?secretId=$s", """{"replication":{"automatic":{}}}""").ok("Create secret") }
            step("add version") { g("POST", "/v1/projects/floci-local/secrets/$s:addVersion", """{"payload":{"data":"${b64("shh")}"}}""").ok("Add version") }
            step("access latest") { need(obj(g("GET", "/v1/projects/floci-local/secrets/$s/versions/latest:access").ok("Access")).str("payload", "data") == b64("shh"), "payload differs") }
            step("delete secret") { g("DELETE", "/v1/projects/floci-local/secrets/$s").ok("Delete secret") }
        },

        Suite("pubsub", "gcp", "Pub/Sub") {
            val topic = "nimbo-fit-$tag"
            val sub = "nimbo-fit-sub-$tag"
            val base = "/v1/projects/floci-local"
            cleanup.add { g("DELETE", "$base/subscriptions/$sub"); g("DELETE", "$base/topics/$topic") }
            var ackId = ""
            step("create topic") { g("PUT", "$base/topics/$topic").ok("Create topic") }
            step("create subscription") { g("PUT", "$base/subscriptions/$sub", """{"topic":"projects/floci-local/topics/$topic"}""").ok("Create subscription") }
            step("publish") { need("messageIds" in g("POST", "$base/topics/$topic:publish", """{"messages":[{"data":"${b64("hi")}"}]}""").ok("Publish").body, "no messageIds") }
            step("pull returns the message") {
                val reply = obj(g("POST", "$base/subscriptions/$sub:pull", """{"maxMessages":5}""").ok("Pull"))
                val first = reply["receivedMessages"]?.jsonArray?.firstOrNull()?.jsonObject ?: throw StepFailed("nothing pulled")
                need(first.str("message", "data") == b64("hi"), "payload differs")
                ackId = first.str("ackId") ?: throw StepFailed("no ackId")
            }
            step("acknowledge") { g("POST", "$base/subscriptions/$sub:acknowledge", """{"ackIds":["$ackId"]}""").ok("Acknowledge") }
            step("delete subscription and topic") { g("DELETE", "$base/subscriptions/$sub").ok("Delete subscription"); g("DELETE", "$base/topics/$topic").ok("Delete topic") }
        },
    )
}
