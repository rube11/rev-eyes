#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage:
  deploy-scheduler.sh validate BACKEND_BASE_URL SCHEDULER_SECRET_ARN BACKEND_SOURCE_CIDR
  deploy-scheduler.sh deploy   BACKEND_BASE_URL SCHEDULER_SECRET_ARN BACKEND_SOURCE_CIDR

BACKEND_SOURCE_CIDR must be the static public IPv4 address of the backend
followed by /32.
EOF
}

if [[ $# -ne 4 ]]; then
  usage
  exit 2
fi

action=$1
backend_base_url=$2
scheduler_secret_arn=$3
backend_source_cidr=$4

case "$action" in
  validate|deploy) ;;
  *)
    usage
    exit 2
    ;;
esac

if [[ "$backend_base_url" != https://* || "$backend_base_url" == */ ]]; then
  echo "backend base URL must use HTTPS and must not end with a slash" >&2
  exit 2
fi

if [[ "$scheduler_secret_arn" != arn:*:secretsmanager:*:*:secret:* ]]; then
  echo "scheduler secret ARN is not a Secrets Manager ARN" >&2
  exit 2
fi
if [[ ! "$backend_source_cidr" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/32$ ]]; then
  echo "backend source CIDR must be a single IPv4 /32" >&2
  exit 2
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
template_file="$script_dir/scheduler.yaml"
region=${AWS_REGION:-${AWS_DEFAULT_REGION:-us-west-2}}
stack_name=${REV_EYES_SCHEDULER_STACK_NAME:-rev-eyes-scheduler}
resource_prefix=${REV_EYES_RESOURCE_PREFIX:-rev-eyes}

account_id=$(aws sts get-caller-identity \
  --query Account \
  --output text \
  --region "$region")

cloudformation_role_arn="arn:aws:iam::$account_id:role/$resource_prefix-scheduler-cloudformation-execution"
schedule_target_role_arn="arn:aws:iam::$account_id:role/$resource_prefix-scheduler-schedule-target"
scheduler_invocation_role_arn="arn:aws:iam::$account_id:role/$resource_prefix-scheduler-api-destination"
schedule_registrar_role_arn="arn:aws:iam::$account_id:role/$resource_prefix-scheduler-registrar"

IFS=: read -r arn_label partition service secret_region secret_account secret_resource \
  <<<"$scheduler_secret_arn"
if [[ "$arn_label" != arn || "$service" != secretsmanager ]]; then
  echo "scheduler secret ARN is not a Secrets Manager ARN" >&2
  exit 2
fi
if [[ "$secret_region" != "$region" ]]; then
  echo "scheduler secret must be in deployment region $region" >&2
  exit 2
fi
if [[ "$secret_account" != "$account_id" ]]; then
  echo "scheduler secret must belong to AWS account $account_id" >&2
  exit 2
fi
if [[ "$secret_resource" != secret:* ]]; then
  echo "scheduler secret ARN has an invalid resource component" >&2
  exit 2
fi

aws cloudformation validate-template \
  --template-body "file://$template_file" \
  --region "$region" \
  >/dev/null

curl --fail --silent --show-error \
  --connect-timeout 5 \
  --max-time 10 \
  "$backend_base_url/health" \
  >/dev/null

scheduler_status=$(curl --silent --show-error \
  --connect-timeout 5 \
  --max-time 10 \
  --request POST \
  --output /dev/null \
  --write-out '%{http_code}' \
  "$backend_base_url/internal/scheduler/run")
if [[ "$scheduler_status" != 401 ]]; then
  echo "unauthenticated scheduler probe returned HTTP $scheduler_status, expected 401" >&2
  exit 1
fi

echo "scheduler preflight passed for account $account_id in $region"
if [[ "$action" == validate ]]; then
  exit 0
fi

change_set_name="$stack_name-deploy-$(date +%s)-$$"
change_set_arn=$(aws cloudformation create-change-set \
  --stack-name "$stack_name" \
  --change-set-name "$change_set_name" \
  --change-set-type UPDATE \
  --template-body "file://$template_file" \
  --region "$region" \
  --role-arn "$cloudformation_role_arn" \
  --parameters \
    "ParameterKey=BackendBaseUrl,ParameterValue=$backend_base_url" \
    "ParameterKey=SchedulerSecretArn,ParameterValue=$scheduler_secret_arn" \
    "ParameterKey=ResourcePrefix,ParameterValue=$resource_prefix" \
    "ParameterKey=BackendSourceCidr,ParameterValue=$backend_source_cidr" \
    "ParameterKey=ScheduleTargetRoleArn,ParameterValue=$schedule_target_role_arn" \
    "ParameterKey=SchedulerInvocationRoleArn,ParameterValue=$scheduler_invocation_role_arn" \
    "ParameterKey=ScheduleRegistrarExecutionRoleArn,ParameterValue=$schedule_registrar_role_arn" \
  --tags Key=Application,Value=rev-eyes \
  --query Id \
  --output text)

if ! aws cloudformation wait change-set-create-complete \
  --change-set-name "$change_set_arn" \
  --region "$region"; then
  change_set_status=$(aws cloudformation describe-change-set \
    --change-set-name "$change_set_arn" \
    --region "$region" \
    --query Status \
    --output text)
  change_set_reason=$(aws cloudformation describe-change-set \
    --change-set-name "$change_set_arn" \
    --region "$region" \
    --query StatusReason \
    --output text)
  if [[ "$change_set_status" == FAILED && "$change_set_reason" == *"didn't contain changes"* ]]; then
    aws cloudformation delete-change-set \
      --change-set-name "$change_set_arn" \
      --region "$region"
    echo "scheduler stack already matches the template"
    exit 0
  fi
  echo "change set failed: $change_set_reason" >&2
  exit 1
fi

aws cloudformation describe-change-set \
  --change-set-name "$change_set_arn" \
  --region "$region" \
  --query 'Changes[].ResourceChange.{Action:Action,LogicalId:LogicalResourceId,Type:ResourceType,Replacement:Replacement}' \
  --output table

aws cloudformation execute-change-set \
  --change-set-name "$change_set_arn" \
  --region "$region"

aws cloudformation wait stack-update-complete \
  --stack-name "$stack_name" \
  --region "$region"

aws cloudformation describe-stacks \
  --stack-name "$stack_name" \
  --region "$region" \
  --query 'Stacks[0].{Status:StackStatus,Outputs:Outputs}' \
  --output json
