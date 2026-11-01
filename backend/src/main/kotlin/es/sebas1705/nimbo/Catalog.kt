package es.sebas1705.nimbo

object Catalog {
    val providers: List<ProviderDef> = listOf(
        ProviderDef(
            id = "floci-aws",
            name = "Floci AWS",
            cloud = Cloud.AWS,
            description = "AWS emulator (S3, SQS, SNS, DynamoDB, Lambda...). MIT.",
            image = "floci/floci",
            ports = listOf(4566),
            endpoints = mapOf(
                "AWS_ENDPOINT_URL" to "http://localhost:4566",
                "AWS_REGION" to "us-east-1",
                // Floci accepts any credentials; these are dummy values, not secrets.
                "AWS_ACCESS_KEY_ID" to "test",
                "AWS_SECRET_ACCESS_KEY" to "test",
            ),
        ),
        ProviderDef(
            id = "floci-azure",
            name = "Floci Azure",
            cloud = Cloud.AZURE,
            description = "Azure emulator. MIT.",
            image = "floci/floci-az",
            ports = listOf(4577),
            // One port, path-style routing. The account key is Azurite's documented development key; set it in your client.
            endpoints = mapOf(
                "NIMBO_AZURE_ENDPOINT" to "http://localhost:4577",
                "AZURE_STORAGE_BLOB_ENDPOINT" to "http://localhost:4577/devstoreaccount1",
                "AZURE_STORAGE_QUEUE_ENDPOINT" to "http://localhost:4577/devstoreaccount1-queue",
                "AZURE_STORAGE_TABLE_ENDPOINT" to "http://localhost:4577/devstoreaccount1-table",
            ),
        ),
        ProviderDef(
            id = "floci-gcp",
            name = "Floci GCP",
            cloud = Cloud.GCP,
            description = "Google Cloud emulator. MIT.",
            image = "floci/floci-gcp",
            ports = listOf(4588),
            endpoints = mapOf(
                "NIMBO_GCP_ENDPOINT" to "http://localhost:4588",
                "GOOGLE_CLOUD_PROJECT" to "floci-local",
                "PUBSUB_EMULATOR_HOST" to "localhost:4588",
                "STORAGE_EMULATOR_HOST" to "http://localhost:4588",
                "SECRET_MANAGER_EMULATOR_HOST" to "localhost:4588",
            ),
        ),
        ProviderDef(
            id = "floci-oci",
            name = "Floci OCI",
            cloud = Cloud.OCI,
            description = "Oracle Cloud emulator. MIT.",
            image = "floci/floci-oci",
            ports = listOf(4599),
            endpoints = mapOf("NIMBO_OCI_ENDPOINT" to "http://localhost:4599"),
        ),
        ProviderDef(
            id = "azurite",
            name = "Azurite",
            cloud = Cloud.AZURE,
            description = "Official Azure Storage emulator (Blob, Queue, Table).",
            image = "mcr.microsoft.com/azure-storage/azurite",
            ports = listOf(10000, 10001, 10002),
            endpoints = mapOf(
                "AZURE_STORAGE_CONNECTION_STRING" to
                    "UseDevelopmentStorage=true",
            ),
        ),
        ProviderDef(
            id = "minio",
            name = "MinIO",
            cloud = Cloud.OTHER,
            description = "S3-compatible object storage, handy as a Terraform state backend.",
            image = "minio/minio",
            ports = listOf(9000, 9001),
            command = listOf("server", "/data", "--console-address", ":9001"),
            secretEnv = mapOf(
                "MINIO_ROOT_USER" to "NIMBO_MINIO_USER",
                "MINIO_ROOT_PASSWORD" to "NIMBO_MINIO_PASSWORD",
            ),
            endpoints = mapOf("MINIO_ENDPOINT" to "http://localhost:9000"),
        ),
        ProviderDef(
            id = "redpanda",
            name = "Redpanda",
            cloud = Cloud.OTHER,
            description = "Kafka-compatible streaming, single node.",
            image = "redpandadata/redpanda",
            ports = listOf(9092),
            command = listOf(
                "redpanda", "start", "--overprovisioned", "--smp", "1", "--memory", "1G",
                "--reserve-memory", "0M", "--node-id", "0", "--check=false",
                "--kafka-addr", "PLAIN://0.0.0.0:9092",
                "--advertise-kafka-addr", "PLAIN://localhost:9092",
            ),
            endpoints = mapOf("KAFKA_BOOTSTRAP_SERVERS" to "localhost:9092"),
        ),
        ProviderDef(
            id = "postgres",
            name = "PostgreSQL",
            cloud = Cloud.OTHER,
            description = "Relational database for RDS / Cloud SQL style practice.",
            image = "postgres:17-alpine",
            ports = listOf(5432),
            secretEnv = mapOf("POSTGRES_PASSWORD" to "NIMBO_POSTGRES_PASSWORD"),
            endpoints = mapOf("DATABASE_HOST" to "localhost", "DATABASE_PORT" to "5432"),
        ),
        ProviderDef(
            id = "toxiproxy",
            name = "Toxiproxy",
            cloud = Cloud.OTHER,
            description = "Chaos proxy: latency, outages and resets in front of the emulators (see the Chaos tab).",
            image = "ghcr.io/shopify/toxiproxy",
            ports = listOf(ChaosTargets.API_PORT) + ChaosTargets.all.map { it.proxyPort },
            // Lets the proxy reach emulators published on the host; Docker Desktop resolves it already, Linux needs the flag.
            runArgs = listOf("--add-host=host.docker.internal:host-gateway"),
            endpoints = mapOf("TOXIPROXY_URL" to "http://localhost:${ChaosTargets.API_PORT}"),
        ),
        ProviderDef(
            id = "redis",
            name = "Redis",
            cloud = Cloud.OTHER,
            description = "Cache / queue practice (ElastiCache, Memorystore).",
            image = "redis:7-alpine",
            ports = listOf(6379),
            endpoints = mapOf("REDIS_URL" to "redis://localhost:6379"),
        ),
    )

    fun find(id: String): ProviderDef? = providers.firstOrNull { it.id == id }
}
