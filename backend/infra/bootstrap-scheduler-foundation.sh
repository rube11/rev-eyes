#!/usr/bin/env bash
set -euo pipefail
umask 077
export AWS_PAGER=""

if [[ $# -ne 1 ]]; then
  echo "usage: $0 SCHEDULER_SECRET_ARN" >&2
  exit 2
fi

scheduler_secret_arn=$1
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
foundation_template="$script_dir/scheduler-foundation.yaml"
deploy_policy="$script_dir/rev-eyes-agent-scheduler-deploy-policy.json"
region=${AWS_REGION:-${AWS_DEFAULT_REGION:-us-west-2}}
account_id=$(aws sts get-caller-identity \
  --query Account \
  --output text \
  --region "$region")
caller_arn=$(aws sts get-caller-identity \
  --query Arn \
  --output text \
  --region "$region")

if [[ "$account_id" != 809411919701 ]]; then
  echo "expected AWS account 809411919701, got $account_id" >&2
  exit 1
fi
if [[ "$caller_arn" != "arn:aws:iam::$account_id:root" ]]; then
  echo "this one-time bootstrap must run from the account root session" >&2
  exit 1
fi
if [[ "$scheduler_secret_arn" != arn:*:secretsmanager:"$region":"$account_id":secret:* ]]; then
  echo "scheduler secret ARN must belong to account $account_id in $region" >&2
  exit 2
fi

aws cloudformation validate-template \
  --template-body "file://$foundation_template" \
  --region "$region" \
  >/dev/null

aws cloudformation deploy \
  --stack-name rev-eyes-scheduler-foundation \
  --template-file "$foundation_template" \
  --region "$region" \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    "SchedulerSecretArn=$scheduler_secret_arn" \
    ResourcePrefix=rev-eyes \
    ApplicationStackName=rev-eyes-scheduler \
  --tags Application=rev-eyes ManagedBy=root-foundation \
  --no-fail-on-empty-changeset

aws cloudformation update-termination-protection \
  --stack-name rev-eyes-scheduler-foundation \
  --enable-termination-protection \
  --region "$region" \
  >/dev/null

scheduler_policy_arn="arn:aws:iam::$account_id:policy/RevEyesSchedulerDeploy"
aws iam create-policy-version \
  --policy-arn "$scheduler_policy_arn" \
  --policy-document "file://$deploy_policy" \
  --set-as-default \
  --query 'PolicyVersion.{VersionId:VersionId,IsDefaultVersion:IsDefaultVersion}' \
  --output table

aws iam attach-user-policy \
  --user-name rev-eyes-agent \
  --policy-arn "$scheduler_policy_arn"

lightsail_policy_arn="arn:aws:iam::$account_id:policy/RevEyesLightsailDeploy"
aws iam detach-user-policy \
  --user-name rev-eyes-agent \
  --policy-arn "$lightsail_policy_arn"

registrar_policy_arn="arn:aws:iam::$account_id:policy/RevEyesSchedulerRegistrarDeploy"
aws iam detach-user-policy \
  --user-name rev-eyes-agent \
  --policy-arn "$registrar_policy_arn"

aws iam list-attached-user-policies \
  --user-name rev-eyes-agent \
  --query 'AttachedPolicies[].PolicyName' \
  --output table
