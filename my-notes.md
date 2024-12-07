# My Notes | Commands and stuff I will find throughout the jorney of learning kro.

## Commands
Before installing kro with helm, you need to login into the public's aws container registry.

aws ecr-public get-login-password --region us-east-1 | docker login --username AWS --password-stdin public.ecr.aws