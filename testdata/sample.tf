terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = "eu-west-1"
}

# web server instance
resource "aws_instance" "web" {
  ami           = "ami-123456"
  instance_type = "t3.micro"

  tags = {
    Name  = "web"
    Owner = "platform"
  }

  ports = [80, 443]

  user_data = <<-EOF
    #!/bin/bash
    echo "hello"
  EOF
}

# database instance
resource "aws_instance" "db" {
  ami           = var.base_ami
  instance_type = "t3.small"
}

output "web_ports" {
  value = aws_instance.web.ports
}
