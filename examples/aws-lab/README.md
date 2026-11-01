# AWS lab (S3 + SQS + DynamoDB) on Floci

Verified against Floci 2.4.0 and OpenTofu (official `ghcr.io/opentofu/opentofu` image): `init`, `validate`, `apply` (3 resources), a clean second `plan` and `destroy` all pass.

1. In Nimbo, bring up the `aws-basic` stack (or just Floci AWS).
2. Export the stack as **OpenTofu / Terraform** and save it next to `main.tf` as `nimbo_override.tf`.
3. Run:

```bash
tofu init
tofu apply
tofu destroy
```

Notes from the first run:

- The override works whether or not `main.tf` declares a `provider "aws"` block.
- `aws_sqs_queue` took about 25 s to create because the AWS provider polls queue attributes until they settle.
- Floci logs warnings about `unix://localhost:2375`: it wants the Docker socket for Lambda and EC2, which Nimbo does not mount yet.
- If you run `tofu` inside a container instead of on the host, share the emulator's network namespace so `localhost:4566` resolves: `docker run --network container:nimbo-floci-aws ...`.
