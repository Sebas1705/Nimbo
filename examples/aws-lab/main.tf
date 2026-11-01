terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}


resource "aws_s3_bucket" "lab" {
  bucket = "nimbo-lab-bucket"
}

resource "aws_sqs_queue" "lab" {
  name = "nimbo-lab-queue"
}

resource "aws_dynamodb_table" "lab" {
  name         = "nimbo-lab-table"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }
}
