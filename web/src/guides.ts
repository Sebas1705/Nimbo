import type { Lang } from './i18n'
import type { TabId } from './nav'

export type L10n = Record<Lang, string>

export interface Snippet {
  title: L10n
  /** Commands or config to copy. Never contains a real secret: placeholders say where yours goes. */
  code: string
}

export interface Endpoint {
  /** A translated label, or a literal one (a protocol or variable name such as "Blob" or "PUBSUB_EMULATOR_HOST"). */
  label: L10n | string
  value: string
}

export interface ProviderGuide {
  id: string
  /** One line for the card. */
  blurb: L10n
  /** What it is, in plain words. */
  what: L10n
  /** When it is the right tool. */
  useFor: L10n
  /** Where it listens and what to put in your own code. */
  endpoints: Endpoint[]
  snippets: Snippet[]
  /** Limits and surprises worth knowing before they cost you an hour. */
  watchOut: L10n[]
  next?: { tab: TabId; label: L10n }
}

const t = (es: string, en: string): L10n => ({ es, en })

export const PROVIDER_GUIDES: ProviderGuide[] = [
  {
    id: 'floci-aws',
    blurb: t('AWS en tu máquina: S3, SQS, SNS, DynamoDB, KMS, Step Functions y más.', 'AWS on your machine: S3, SQS, SNS, DynamoDB, KMS, Step Functions and more.'),
    what: t(
      'Un emulador de AWS que responde a las mismas llamadas que la nube real. Nimbo lo comprueba con 13 pruebas: S3, SQS, SNS, DynamoDB, SSM, Secrets Manager, KMS, EventBridge, Kinesis, CloudWatch Logs, IAM y STS, Step Functions y Lambda.',
      'An AWS emulator that answers the same calls as the real cloud. Nimbo checks it with 13 tests: S3, SQS, SNS, DynamoDB, SSM, Secrets Manager, KMS, EventBridge, Kinesis, CloudWatch Logs, IAM and STS, Step Functions and Lambda.',
    ),
    useFor: t(
      'Practicar código o Terraform pensado para AWS sin cuenta ni factura. Cambias una dirección y el resto de tu código no se entera.',
      'Practising code or Terraform written for AWS with no account and no bill. You change one address and the rest of your code never notices.',
    ),
    endpoints: [
      { label: t('Dirección', 'Endpoint'), value: 'http://localhost:4566' },
      { label: t('Región', 'Region'), value: 'us-east-1' },
      { label: t('Credenciales (vale cualquiera)', 'Credentials (anything works)'), value: 'test / test' },
    ],
    snippets: [
      {
        title: t('Con el AWS CLI', 'With the AWS CLI'),
        code: `export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
aws --endpoint-url http://localhost:4566 s3 mb s3://mi-bucket
aws --endpoint-url http://localhost:4566 sqs create-queue --queue-name mi-cola
aws --endpoint-url http://localhost:4566 s3 ls`,
      },
      {
        title: t('En tu propio código', 'In your own code'),
        code: `# Los SDK recientes leen esta variable y no hace falta tocar el código
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1`,
      },
      {
        title: t('Con OpenTofu o Terraform', 'With OpenTofu or Terraform'),
        code: `# 1. Infraestructura > tu stack > Exportar > OpenTofu / Terraform
# 2. Guarda el resultado junto a tus .tf con el nombre nimbo_override.tf
tofu init
tofu apply`,
      },
    ],
    watchOut: [
      t('Lambda se puede crear, pero invocarla necesita el socket de Docker y Nimbo todavía no lo monta.', 'Lambda can be created, but invoking it needs the Docker socket, which Nimbo does not mount yet.'),
      t('Con Terraform, una cola SQS tarda unos 25 segundos en crearse: el proveedor espera a que se asienten sus atributos. No es un fallo.', 'With Terraform, an SQS queue takes about 25 seconds to create: the provider waits for its attributes to settle. It is not a failure.'),
      t('Trata los datos como desechables: no están pensados para sobrevivir a borrar el contenedor.', 'Treat the data as disposable: it is not meant to outlive the container.'),
    ],
    next: { tab: 'inspector', label: t('Ver lo que has creado en el Inspector', 'See what you created in the Inspector') },
  },
  {
    id: 'floci-azure',
    blurb: t('Azure en un solo puerto: Blob, Queue y Table Storage, y más.', 'Azure on a single port: Blob, Queue and Table Storage, and more.'),
    what: t(
      'Un emulador de Azure que sirve casi todo por el puerto 4577, con rutas distintas por servicio. Nimbo comprueba Blob, Queue y Table Storage.',
      'An Azure emulator that serves nearly everything on port 4577, with a different path per service. Nimbo checks Blob, Queue and Table Storage.',
    ),
    useFor: t(
      'Probar código que usa Azure Storage (y, con la documentación de Floci, otros servicios) sin suscripción.',
      'Trying code that uses Azure Storage (and, with Floci\'s documentation, other services) without a subscription.',
    ),
    endpoints: [
      { label: t('Base', 'Base'), value: 'http://localhost:4577' },
      { label: 'Blob', value: 'http://localhost:4577/devstoreaccount1' },
      { label: 'Queue', value: 'http://localhost:4577/devstoreaccount1-queue' },
      { label: 'Table', value: 'http://localhost:4577/devstoreaccount1-table' },
      { label: t('Cuenta', 'Account'), value: 'devstoreaccount1' },
    ],
    snippets: [
      {
        title: t('Crear un contenedor con curl', 'Create a container with curl'),
        code: `curl -sS -X PUT "http://localhost:4577/devstoreaccount1/mi-contenedor?restype=container" \\
  -H "x-ms-version: 2021-08-06" \\
  -H "x-ms-date: $(date -u '+%a, %d %b %Y %H:%M:%S GMT')" \\
  -H "Authorization: SharedKey devstoreaccount1:x"`,
      },
      {
        title: t('Cadena de conexión para un SDK', 'Connection string for an SDK'),
        code: `DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=<la clave de desarrollo de Azurite>;BlobEndpoint=http://localhost:4577/devstoreaccount1;QueueEndpoint=http://localhost:4577/devstoreaccount1-queue;TableEndpoint=http://localhost:4577/devstoreaccount1-table;`,
      },
    ],
    watchOut: [
      t('La clave de la cuenta es la de desarrollo de Azurite, que Microsoft publica en su documentación. Floci no la valida.', 'The account key is the Azurite development key, which Microsoft publishes in its documentation. Floci does not validate it.'),
      t('OpenTofu y Terraform con el proveedor azurerm exigen TLS, así que no funcionan con este emulador por HTTP.', 'OpenTofu and Terraform with the azurerm provider require TLS, so they do not work with this emulator over HTTP.'),
      t('Functions y Event Hubs necesitan el socket de Docker, que Nimbo no monta todavía.', 'Functions and Event Hubs need the Docker socket, which Nimbo does not mount yet.'),
    ],
    next: { tab: 'fidelity', label: t('Ver qué parte de Azure está comprobada', 'See which part of Azure is checked') },
  },
  {
    id: 'floci-gcp',
    blurb: t('Google Cloud: Pub/Sub, Cloud Storage, Secret Manager y más.', 'Google Cloud: Pub/Sub, Cloud Storage, Secret Manager and more.'),
    what: t(
      'Un emulador de Google Cloud en el puerto 4588 (REST y gRPC). Nimbo comprueba Cloud Storage, Secret Manager y Pub/Sub, y puede mostrar sus temas y suscripciones en el Inspector.',
      'A Google Cloud emulator on port 4588 (REST and gRPC). Nimbo checks Cloud Storage, Secret Manager and Pub/Sub, and can show its topics and subscriptions in the Inspector.',
    ),
    useFor: t(
      'Practicar mensajería con Pub/Sub o almacenamiento con Cloud Storage sin proyecto de Google.',
      'Practising messaging with Pub/Sub or storage with Cloud Storage with no Google project.',
    ),
    endpoints: [
      { label: t('Dirección', 'Endpoint'), value: 'http://localhost:4588' },
      { label: t('Proyecto', 'Project'), value: 'floci-local' },
      { label: 'PUBSUB_EMULATOR_HOST', value: 'localhost:4588' },
      { label: 'STORAGE_EMULATOR_HOST', value: 'http://localhost:4588' },
    ],
    snippets: [
      {
        title: t('Pub/Sub con curl: tema, suscripción y mensaje', 'Pub/Sub with curl: topic, subscription and message'),
        code: `B=http://localhost:4588/v1/projects/floci-local
curl -sS -X PUT $B/topics/pedidos
curl -sS -X PUT $B/subscriptions/facturacion -H 'Content-Type: application/json' \\
  -d '{"topic":"projects/floci-local/topics/pedidos"}'
curl -sS -X POST $B/topics/pedidos:publish -H 'Content-Type: application/json' \\
  -d '{"messages":[{"data":"aG9sYQ=="}]}'
curl -sS -X POST $B/subscriptions/facturacion:pull -H 'Content-Type: application/json' \\
  -d '{"maxMessages":5}'`,
      },
      {
        title: t('Para las librerías oficiales de Google', 'For the official Google libraries'),
        code: `export GOOGLE_CLOUD_PROJECT=floci-local
export PUBSUB_EMULATOR_HOST=localhost:4588
export STORAGE_EMULATOR_HOST=http://localhost:4588`,
      },
    ],
    watchOut: [
      t('Los datos de un mensaje de Pub/Sub van en base64: "aG9sYQ==" es "hola".', 'The data of a Pub/Sub message is base64: "aG9sYQ==" is "hola".'),
      t('Firestore solo se sirve por gRPC, así que Nimbo no lo comprueba ni lo muestra.', 'Firestore is only served over gRPC, so Nimbo neither checks nor shows it.'),
    ],
    next: { tab: 'inspector', label: t('Ver los temas y suscripciones en el Inspector', 'See topics and subscriptions in the Inspector') },
  },
  {
    id: 'floci-oci',
    blurb: t('Oracle Cloud. Experimental: Nimbo lo arranca pero aún no lo comprueba.', 'Oracle Cloud. Experimental: Nimbo starts it but does not check it yet.'),
    what: t(
      'Un emulador de Oracle Cloud en el puerto 4599. Es el único de la familia Floci que Nimbo todavía no ha probado a fondo.',
      'An Oracle Cloud emulator on port 4599. It is the only one in the Floci family that Nimbo has not tested in depth yet.',
    ),
    useFor: t(
      'Explorar OCI sin cuenta. Si lo usas de verdad, la documentación de Floci es tu referencia.',
      'Exploring OCI without an account. If you rely on it, the Floci documentation is your reference.',
    ),
    endpoints: [{ label: t('Dirección', 'Endpoint'), value: 'http://localhost:4599' }],
    snippets: [],
    watchOut: [
      t('No hay pruebas de fidelidad ni inspector para OCI: lo que funciona lo descubres tú.', 'There are no fidelity checks or inspector for OCI: you find out what works.'),
    ],
  },
  {
    id: 'azurite',
    blurb: t('El emulador oficial de Microsoft para Azure Storage.', 'Microsoft\'s official Azure Storage emulator.'),
    what: t(
      'El emulador que Microsoft mantiene para Blob, Queue y Table Storage, cada uno en su puerto.',
      'The emulator Microsoft maintains for Blob, Queue and Table Storage, each on its own port.',
    ),
    useFor: t(
      'Cuando solo necesitas Azure Storage y quieres la implementación oficial. Para el resto de Azure, usa Floci Azure.',
      'When you only need Azure Storage and want the official implementation. For the rest of Azure, use Floci Azure.',
    ),
    endpoints: [
      { label: 'Blob', value: 'http://localhost:10000' },
      { label: 'Queue', value: 'http://localhost:10001' },
      { label: 'Table', value: 'http://localhost:10002' },
    ],
    snippets: [
      {
        title: t('Con el Azure CLI', 'With the Azure CLI'),
        code: `az storage container create --name demo --connection-string "UseDevelopmentStorage=true"
az storage container list --connection-string "UseDevelopmentStorage=true" -o table`,
      },
      {
        title: t('Cadena de conexión', 'Connection string'),
        code: 'UseDevelopmentStorage=true',
      },
    ],
    watchOut: [
      t('Es un emulador de almacenamiento: no tiene Key Vault, Service Bus ni Functions.', 'It is a storage emulator: no Key Vault, Service Bus or Functions.'),
    ],
  },
  {
    id: 'minio',
    blurb: t('Almacenamiento de objetos compatible con S3, con consola web.', 'S3-compatible object storage with a web console.'),
    what: t(
      'Un servidor de almacenamiento de objetos que habla el protocolo de S3, con su propia consola web.',
      'An object storage server that speaks the S3 protocol, with its own web console.',
    ),
    useFor: t(
      'Cualquier cosa que necesite "un S3" que persista algo más que un emulador de AWS: copias, artefactos, datos de prueba.',
      'Anything that needs "an S3" that lasts longer than an AWS emulator: backups, artifacts, test data.',
    ),
    endpoints: [
      { label: 'API', value: 'http://localhost:9000' },
      { label: t('Consola web', 'Web console'), value: 'http://localhost:9001' },
      { label: t('Usuario y contraseña', 'User and password'), value: 'NIMBO_MINIO_USER / NIMBO_MINIO_PASSWORD' },
    ],
    snippets: [
      {
        title: t('Con el AWS CLI', 'With the AWS CLI'),
        code: `export AWS_ACCESS_KEY_ID=$NIMBO_MINIO_USER AWS_SECRET_ACCESS_KEY=$NIMBO_MINIO_PASSWORD AWS_REGION=us-east-1
aws --endpoint-url http://localhost:9000 s3 mb s3://copias
aws --endpoint-url http://localhost:9000 s3 ls`,
      },
    ],
    watchOut: [
      t('No arranca sin NIMBO_MINIO_USER y NIMBO_MINIO_PASSWORD (la contraseña, de al menos 8 caracteres). Defínelas en un .env o con Doppler; usa valores de usar y tirar.', 'It does not start without NIMBO_MINIO_USER and NIMBO_MINIO_PASSWORD (the password needs at least 8 characters). Set them in a .env or with Doppler; use throwaway values.'),
    ],
  },
  {
    id: 'redpanda',
    blurb: t('Un broker compatible con Kafka, de un solo nodo.', 'A single-node, Kafka-compatible broker.'),
    what: t(
      'Un broker de streaming que habla el protocolo de Kafka. Trae su propia herramienta de línea de comandos, rpk.',
      'A streaming broker that speaks the Kafka protocol. It ships its own command line tool, rpk.',
    ),
    useFor: t(
      'Practicar productores, consumidores y particiones de Kafka sin montar un clúster.',
      'Practising Kafka producers, consumers and partitions without building a cluster.',
    ),
    endpoints: [{ label: 'Bootstrap servers', value: 'localhost:9092' }],
    snippets: [
      {
        title: t('Crear un tema, escribir y leer con rpk', 'Create a topic, write and read with rpk'),
        code: `docker exec nimbo-redpanda rpk topic create pedidos
echo "hola" | docker exec -i nimbo-redpanda rpk topic produce pedidos
docker exec nimbo-redpanda rpk topic consume pedidos -n 1`,
      },
      {
        title: t('En tu propio código', 'In your own code'),
        code: 'export KAFKA_BOOTSTRAP_SERVERS=localhost:9092',
      },
    ],
    watchOut: [
      t('No pasa por Toxiproxy: los clientes de Kafka se reconectan a la dirección que anuncia el broker, así que el Caos no le alcanza.', 'It does not go through Toxiproxy: Kafka clients reconnect to the address the broker advertises, so Chaos cannot reach it.'),
    ],
  },
  {
    id: 'postgres',
    blurb: t('PostgreSQL 17, para practicar como si fuera RDS o Cloud SQL.', 'PostgreSQL 17, to practise as if it were RDS or Cloud SQL.'),
    what: t('Una base de datos PostgreSQL real, con el usuario postgres.', 'A real PostgreSQL database, with the postgres user.'),
    useFor: t(
      'Aprender migraciones, conexiones y rendimiento contra una base de datos de verdad sin pagar una instancia.',
      'Learning migrations, connections and performance against a real database without paying for an instance.',
    ),
    endpoints: [
      { label: t('Servidor', 'Server'), value: 'localhost:5432' },
      { label: t('Usuario', 'User'), value: 'postgres' },
      { label: t('Contraseña', 'Password'), value: 'NIMBO_POSTGRES_PASSWORD' },
    ],
    snippets: [
      {
        title: t('Entrar con psql', 'Connect with psql'),
        code: `docker exec -it nimbo-postgres psql -U postgres
# o desde tu máquina, si tienes psql:
PGPASSWORD=$NIMBO_POSTGRES_PASSWORD psql -h localhost -U postgres`,
      },
      { title: t('Para un conector JDBC', 'For a JDBC connector'), code: 'jdbc:postgresql://localhost:5432/postgres' },
    ],
    watchOut: [
      t('No arranca sin NIMBO_POSTGRES_PASSWORD. Defínela en un .env o con Doppler.', 'It does not start without NIMBO_POSTGRES_PASSWORD. Set it in a .env or with Doppler.'),
    ],
  },
  {
    id: 'redis',
    blurb: t('Redis 7, para caché y colas sencillas.', 'Redis 7, for caches and simple queues.'),
    what: t('Un servidor Redis, sin contraseña.', 'A Redis server, no password.'),
    useFor: t(
      'Practicar cachés, contadores, bloqueos distribuidos y colas ligeras, como harías con ElastiCache o Memorystore.',
      'Practising caches, counters, distributed locks and light queues, as you would with ElastiCache or Memorystore.',
    ),
    endpoints: [{ label: 'URL', value: 'redis://localhost:6379' }],
    snippets: [
      {
        title: t('Comprobar que responde', 'Check that it answers'),
        code: `docker exec nimbo-redis redis-cli ping
docker exec nimbo-redis redis-cli set clave valor
docker exec nimbo-redis redis-cli get clave`,
      },
      { title: t('En tu propio código', 'In your own code'), code: 'export REDIS_URL=redis://localhost:6379' },
    ],
    watchOut: [
      t('Está pensado para tu máquina: no tiene contraseña y solo escucha en 127.0.0.1.', 'It is meant for your machine: no password, and it only listens on 127.0.0.1.'),
    ],
  },
  {
    id: 'toxiproxy',
    blurb: t('El proxy que rompe cosas a propósito. Lo maneja la pestaña Caos.', 'The proxy that breaks things on purpose. The Chaos tab drives it.'),
    what: t(
      'Un proxy de red que se pone delante de los emuladores y les añade latencia, cortes o reseteos. No lo usas directamente: la pestaña Caos le habla por ti.',
      'A network proxy that sits in front of the emulators and adds latency, cuts or resets. You do not use it directly: the Chaos tab talks to it for you.',
    ),
    useFor: t(
      'Comprobar que tu código aguanta una dependencia lenta o caída: timeouts, reintentos, idempotencia.',
      'Checking that your code survives a slow or dead dependency: timeouts, retries, idempotency.',
    ),
    endpoints: [
      { label: t('API de control', 'Control API'), value: 'http://localhost:8474' },
      { label: t('Puerto de caos de Floci AWS', 'Chaos port of Floci AWS'), value: 'http://localhost:14566  (en vez de 4566)' },
    ],
    snippets: [
      { title: t('Ver los proxies que existen', 'See the proxies that exist'), code: 'curl -sS http://localhost:8474/proxies' },
    ],
    watchOut: [
      t('Tu código tiene que apuntar al puerto de caos (por ejemplo 14566) para que los fallos le afecten. En Infraestructura, la casilla "Con caos" del export lo hace por ti.', 'Your code has to point at the chaos port (for example 14566) for the faults to affect it. In Infrastructure, the "Through chaos" box of the export does it for you.'),
    ],
    next: { tab: 'chaos', label: t('Ir a la pestaña Caos', 'Go to the Chaos tab') },
  },
  {
    id: 'k3s',
    blurb: t('Un clúster Kubernetes real de un solo nodo.', 'A real single-node Kubernetes cluster.'),
    what: t(
      'k3s, una distribución ligera de Kubernetes, en un contenedor. kubectl vive dentro, así que no instalas nada.',
      'k3s, a lightweight Kubernetes distribution, in one container. kubectl lives inside it, so you install nothing.',
    ),
    useFor: t(
      'Aprender Deployments, Services, probes y rollouts contra un clúster de verdad y desecharlo cuando acabes.',
      'Learning Deployments, Services, probes and rollouts against a real cluster, and throwing it away afterwards.',
    ),
    endpoints: [
      { label: t('API de Kubernetes', 'Kubernetes API'), value: 'https://127.0.0.1:6443' },
      { label: 'NodePort', value: '30080 - 30084  ->  http://localhost:30080' },
    ],
    snippets: [
      {
        title: t('kubectl sin instalar nada', 'kubectl without installing anything'),
        code: `docker exec nimbo-k3s kubectl get nodes
docker exec nimbo-k3s kubectl get pods -A`,
      },
      {
        title: t('Con tu propio kubectl', 'With your own kubectl'),
        code: `# Pestaña Kubernetes > Copiar kubeconfig, guárdalo como nimbo.kubeconfig.yaml
kubectl --kubeconfig nimbo.kubeconfig.yaml get nodes`,
      },
    ],
    watchOut: [
      t('Tarda unos 30 segundos en estar listo la primera vez.', 'It needs about 30 seconds to be ready the first time.'),
      t('Corre en modo privilegiado, que k3s necesita. Páralo cuando no lo uses.', 'It runs privileged, which k3s needs. Stop it when you are not using it.'),
      t('Un Service NodePort solo es accesible desde tu máquina si su puerto está entre 30080 y 30084.', 'A NodePort Service is only reachable from your machine if its port is between 30080 and 30084.'),
    ],
    next: { tab: 'kube', label: t('Ir a la pestaña Kubernetes', 'Go to the Kubernetes tab') },
  },
  {
    id: 'jaeger',
    blurb: t('Dónde ver las trazas distribuidas de tu código.', 'Where to see your code\'s distributed traces.'),
    what: t(
      'Jaeger recibe trazas de OpenTelemetry y las guarda en memoria. La pestaña Trazas las dibuja como una cascada.',
      'Jaeger receives OpenTelemetry traces and keeps them in memory. The Traces tab draws them as a waterfall.',
    ),
    useFor: t(
      'Ver por dónde pasa una petición y dónde se pierde el tiempo, con tu propio código instrumentado o con la traza de ejemplo.',
      'Seeing where a request goes and where the time is lost, with your own instrumented code or the sample trace.',
    ),
    endpoints: [
      { label: t('Interfaz de Jaeger', 'Jaeger UI'), value: 'http://localhost:16686' },
      { label: 'OTLP HTTP', value: 'http://localhost:4318' },
      { label: 'OTLP gRPC', value: 'localhost:4317' },
    ],
    snippets: [
      {
        title: t('Para cualquier SDK de OpenTelemetry', 'For any OpenTelemetry SDK'),
        code: `export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
export OTEL_SERVICE_NAME=mi-servicio`,
      },
    ],
    watchOut: [
      t('Las trazas viven en memoria: si reinicias Jaeger, desaparecen.', 'Traces live in memory: if you restart Jaeger, they are gone.'),
    ],
    next: { tab: 'traces', label: t('Ir a la pestaña Trazas', 'Go to the Traces tab') },
  },
]

export function guideFor(id: string): ProviderGuide | undefined {
  return PROVIDER_GUIDES.find((g) => g.id === id)
}

/** A one-click way to bring up what a typical task needs, with what to do next. */
export interface Recipe {
  id: string
  /** Providers to start, in order. None of them needs a secret, so the recipe works on a fresh install. */
  providers: string[]
  title: L10n
  summary: L10n
  /** What you will have once it is up. */
  youGet: L10n[]
  next: { tab: TabId; label: L10n }
  /** A lab that puts it to use. */
  lab?: string
}

export const RECIPES: Recipe[] = [
  {
    id: 'aws',
    providers: ['floci-aws'],
    title: t('AWS en tu máquina', 'AWS on your machine'),
    summary: t('S3, SQS, DynamoDB y compañía, listos para tu código o tu Terraform.', 'S3, SQS, DynamoDB and friends, ready for your code or your Terraform.'),
    youGet: [
      t('Una dirección de AWS en http://localhost:4566', 'An AWS endpoint at http://localhost:4566'),
      t('Credenciales de mentira: test / test', 'Fake credentials: test / test'),
    ],
    next: { tab: 'inspector', label: t('Mira lo que crees en el Inspector', 'Watch what you create in the Inspector') },
    lab: 'first-bucket',
  },
  {
    id: 'multicloud',
    providers: ['floci-aws', 'floci-azure', 'floci-gcp'],
    title: t('Las tres nubes a la vez', 'All three clouds at once'),
    summary: t('AWS, Azure y Google Cloud lado a lado, para comparar cómo resuelve cada una lo mismo.', 'AWS, Azure and Google Cloud side by side, to compare how each solves the same thing.'),
    youGet: [
      t('AWS en 4566, Azure en 4577 y Google Cloud en 4588', 'AWS on 4566, Azure on 4577 and Google Cloud on 4588'),
      t('Una matriz de fidelidad para saber qué cubre cada una', 'A fidelity matrix to see what each one covers'),
    ],
    next: { tab: 'fidelity', label: t('Mira qué cubre cada emulador', 'See what each emulator covers') },
    lab: 'pubsub-fanout',
  },
  {
    id: 'events',
    providers: ['floci-aws', 'redpanda', 'redis'],
    title: t('Eventos, colas y caché', 'Events, queues and cache'),
    summary: t('Colas de AWS, un broker de Kafka y Redis para practicar arquitecturas dirigidas por eventos.', 'AWS queues, a Kafka broker and Redis to practise event-driven architectures.'),
    youGet: [
      t('SQS y SNS en 4566, Kafka en 9092 y Redis en 6379', 'SQS and SNS on 4566, Kafka on 9092 and Redis on 6379'),
    ],
    next: { tab: 'inspector', label: t('Mira los mensajes de las colas', 'Look at the messages in the queues') },
    lab: 'queue-it-up',
  },
  {
    id: 'cluster',
    providers: ['k3s', 'jaeger'],
    title: t('Kubernetes y trazas', 'Kubernetes and traces'),
    summary: t('Un clúster real donde desplegar y un lugar donde ver por dónde pasa cada petición.', 'A real cluster to deploy to and a place to see where each request goes.'),
    youGet: [
      t('Kubernetes tarda unos 30 s en estar listo', 'Kubernetes needs about 30 s to be ready'),
      t('Jaeger recibe trazas en http://localhost:4318', 'Jaeger receives traces at http://localhost:4318'),
    ],
    next: { tab: 'kube', label: t('Despliega algo en la pestaña Kubernetes', 'Deploy something in the Kubernetes tab') },
    lab: 'first-cluster',
  },
  {
    id: 'chaos',
    providers: ['floci-aws', 'toxiproxy'],
    title: t('Rompe cosas a propósito', 'Break things on purpose'),
    summary: t('AWS con un proxy delante que añade latencia, cortes y reseteos cuando tú quieras.', 'AWS with a proxy in front that adds latency, cuts and resets whenever you want.'),
    youGet: [
      t('Tu código, apuntando a http://localhost:14566, sufre los fallos que apliques', 'Your code, pointed at http://localhost:14566, suffers the faults you apply'),
    ],
    next: { tab: 'chaos', label: t('Aplica un fallo en la pestaña Caos', 'Apply a fault in the Chaos tab') },
    lab: 'latency-drill',
  },
]

export interface TabHelp {
  points: L10n[]
}

/** A few plain sentences at the top of each screen: how it works and what to try first. */
export const TAB_HELP: Record<TabId, TabHelp> = {
  guide: {
    points: [
      t('Nimbo no emula nada por sí mismo: arranca emuladores existentes en contenedores de Docker y te da un sitio para usarlos.', 'Nimbo emulates nothing by itself: it starts existing emulators in Docker containers and gives you one place to use them.'),
      t('Tu código no cambia: solo apuntas a una dirección local (por ejemplo http://localhost:4566) en vez de a la nube real.', 'Your code does not change: you only point at a local address (for example http://localhost:4566) instead of the real cloud.'),
    ],
  },
  infra: {
    points: [
      t('Un stack es un grupo de proveedores que se arrancan juntos. Un proveedor es un contenedor: un emulador, una base de datos, un clúster.', 'A stack is a group of providers started together. A provider is one container: an emulator, a database, a cluster.'),
      t('Pulsa "Guía" en cualquier proveedor para ver dónde escucha, qué comandos usar y qué límites tiene.', 'Press "Guide" on any provider to see where it listens, which commands to use and what its limits are.'),
      t('"Exportar" en un stack genera las variables de entorno, el override de OpenTofu o el perfil del AWS CLI para conectar tu código.', '"Export" on a stack generates the environment variables, the OpenTofu override or the AWS CLI profile to connect your code.'),
    ],
  },
  inspector: {
    points: [
      t('Solo lectura: aquí miras lo que han creado tu código o tus comandos. Se actualiza solo cada pocos segundos.', 'Read-only: here you look at what your code or your commands created. It refreshes by itself every few seconds.'),
      t('Mirar los mensajes de una cola no los consume, pero cada vistazo sube su contador de recepciones, igual que en SQS real.', 'Looking at a queue\'s messages does not consume them, but each look raises their receive count, just like real SQS.'),
      t('Si una sección dice que el emulador no responde, arranca ese proveedor en Infraestructura.', 'If a section says the emulator is not responding, start that provider in Infrastructure.'),
    ],
  },
  kube: {
    points: [
      t('Arranca el proveedor Kubernetes (k3s) y espera unos 30 segundos a que el nodo esté listo.', 'Start the Kubernetes (k3s) provider and wait about 30 seconds for the node to be ready.'),
      t('Elige un ejemplo en "Aplicar manifiesto" y pulsa Aplicar. El de "Crash loop" te enseña cómo se ve un pod que falla y cómo leer su log.', 'Pick a sample in "Apply a manifest" and press Apply. The "Crash loop" one shows you what a failing pod looks like and how to read its log.'),
      t('Un Service NodePort entre 30080 y 30084 se abre en http://localhost:<puerto>.', 'A NodePort Service between 30080 and 30084 opens at http://localhost:<port>.'),
    ],
  },
  traces: {
    points: [
      t('Una traza es el recorrido de una petición; cada barra es un paso y su longitud, lo que tarda. Las rojas son pasos que fallaron.', 'A trace is the path of one request; each bar is a step and its length is how long it took. Red ones failed.'),
      t('Sin código instrumentado, pulsa "Enviar ejemplo" para ver una traza de muestra.', 'With no instrumented code, press "Send sample" to see a sample trace.'),
      t('Para enviar las tuyas, apunta cualquier SDK de OpenTelemetry a http://localhost:4318.', 'To send your own, point any OpenTelemetry SDK at http://localhost:4318.'),
    ],
  },
  chaos: {
    points: [
      t('Cada emulador tiene un puerto de caos (el normal más 10000: 4566 pasa a 14566). Tu código tiene que usar ese puerto para sufrir los fallos.', 'Each emulator has a chaos port (the normal one plus 10000: 4566 becomes 14566). Your code has to use that port to suffer the faults.'),
      t('Elige un fallo, ajusta sus valores y pulsa Aplicar. Se aplican al instante y se pueden quitar de uno en uno o todos a la vez.', 'Pick a fault, adjust its values and press Apply. They take effect at once and can be removed one by one or all together.'),
      t('Empieza por una latencia de 1500 ms y comprueba que tu cliente aguanta con timeouts y reintentos.', 'Start with 1500 ms of latency and check that your client copes with timeouts and retries.'),
    ],
  },
  fidelity: {
    points: [
      t('Ejecuta operaciones reales (crear, usar, leer y borrar) contra cada emulador y te dice qué funciona entero, a medias o nada.', 'It runs real operations (create, use, read back and delete) against each emulator and tells you what works fully, partly or not at all.'),
      t('"Completo" significa que todos los pasos pasaron; "Parcial", que algunos fallaron (despliega la fila para ver cuáles); "Apagado", que el emulador no está en marcha.', '"Full" means every step passed; "Partial" that some failed (open the row to see which); "Offline" that the emulator is not running.'),
      t('Ejecútala cada vez que actualices un emulador: es la forma de saber si algo dejó de funcionar.', 'Run it every time you update an emulator: it is how you find out whether something stopped working.'),
    ],
  },
  labs: {
    points: [
      t('Cada laboratorio es un ejercicio: tú construyes con tus propias herramientas y Nimbo comprueba el resultado dentro de los emuladores.', 'Each lab is an exercise: you build with your own tools and Nimbo checks the result inside the emulators.'),
      t('Los pasos te dicen qué hacer, las pistas te empujan sin regalarte nada y la solución está plegada por si te atascas.', 'The steps tell you what to do, the hints nudge you without giving it away, and the solution is folded away in case you get stuck.'),
      t('Pulsa Verificar cuando creas haber terminado: te dice qué comprobación falla y por qué.', 'Press Verify when you think you are done: it tells you which check fails and why.'),
    ],
  },
  activity: {
    points: [
      t('Todo se guarda en una carpeta de tu equipo y nunca se envía a ningún sitio: el tiempo en marcha se mide mientras Nimbo está abierto.', 'Everything is kept in a folder on your computer and never sent anywhere: time up is measured while Nimbo is open.'),
      t('El diario muestra lo último que hiciste. La fidelidad compara cada ejecución con la anterior y marca en rojo lo que empeoró.', 'The journal shows the latest things you did. Fidelity compares each run with the previous one and marks in red what got worse.'),
    ],
  },
  settings: {
    points: [
      t('"Conservar datos" se activa por emulador, en Infraestructura: sus datos viven en un volumen de Docker y sobreviven a parar o eliminar el contenedor. Borrarlos es una acción aparte.', '"Keep data" is switched on per emulator, in Infrastructure: its data lives in a Docker volume and survives stopping or removing the container. Deleting it is a separate action.'),
      t('Las contraseñas de MinIO y PostgreSQL se guardan cifradas con tu usuario de Windows. Si ya las defines con una variable de entorno, esa gana. Con PostgreSQL conservado, la contraseña queda fijada al crearlo: cambiarla aquí no cambia la base existente.', 'The MinIO and PostgreSQL passwords are stored encrypted for your Windows user. If you already set them with an environment variable, that one wins. With PostgreSQL data kept, its password is fixed when it is created: changing it here does not change the existing database.'),
      t('La copia de seguridad incluye ajustes, progreso, estadísticas e historial, nunca las contraseñas ni los datos de los emuladores.', 'The backup holds settings, progress, statistics and history, never the passwords or the emulator data.'),
    ],
  },
  updates: {
    points: [
      t('Nimbo solo habla con github.com/Sebas1705/Nimbo, como mucho una vez al día en modo automático y nunca en modo manual. No descarga ni instala nada sin que lo confirmes.', 'Nimbo only talks to github.com/Sebas1705/Nimbo, at most once a day in automatic mode and never in manual mode. It downloads and installs nothing without your confirmation.'),
      t('Cada descarga se comprueba con el SHA256SUMS de la versión y se borra si no coincide. Un instalador importado desde un archivo sin comprobación se marca como "sin verificar".', 'Every download is checked against the release\'s SHA256SUMS and deleted if it does not match. An installer imported from a file with no checksum is marked "not verified".'),
      t('Solo la app de Windows se instala sola. Con el binario portable tendrás el archivo nuevo; con Docker, el comando para actualizar la imagen.', 'Only the Windows app installs itself. With the portable binary you get the new file; with Docker, the command to update the image.'),
    ],
  },
  offline: {
    points: [
      t('Los emuladores son imágenes de Docker que normalmente se descargan al arrancarlos. Aquí ves cuáles ya están en tu equipo.', 'The emulators are Docker images that are normally downloaded when you start them. Here you see which ones are already on your computer.'),
      t('Para trabajar sin internet, prepara un kit con packaging/make-offline-kit.ps1 en un equipo con conexión y carga los paquetes aquí. Cada paquete se verifica si hay un SHA256SUMS a su lado.', 'To work without internet, build a kit with packaging/make-offline-kit.ps1 on a connected computer and load the packs here. Each pack is verified when a SHA256SUMS sits next to it.'),
    ],
  },
}

/** Short stack descriptions in both languages for the stacks that ship; user-defined stacks keep their own text. */
export const STACK_BLURBS: Record<string, L10n> = {
  'aws-basic': t('AWS más almacenamiento compatible con S3. MinIO necesita credenciales (NIMBO_MINIO_*).', 'AWS plus S3-compatible storage. MinIO needs credentials (NIMBO_MINIO_*).'),
  multicloud: t('Los emuladores de AWS, Azure y Google Cloud lado a lado.', 'The AWS, Azure and Google Cloud emulators side by side.'),
  'event-driven': t('Mensajería y datos para arquitecturas de eventos. PostgreSQL necesita NIMBO_POSTGRES_PASSWORD.', 'Messaging and data for event-driven architectures. PostgreSQL needs NIMBO_POSTGRES_PASSWORD.'),
  'storage-lab': t('Almacenamiento de blobs y objetos en dos nubes. MinIO necesita credenciales.', 'Blob and object storage on two clouds. MinIO needs credentials.'),
  cluster: t('Kubernetes (k3s) más trazas con Jaeger.', 'Kubernetes (k3s) plus Jaeger tracing.'),
}

export function stackBlurb(id: string, lang: Lang, fallback: string): string {
  return STACK_BLURBS[id]?.[lang] ?? fallback
}
