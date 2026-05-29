/**
 * thor_doctor — health check.
 */

import { STSClient, GetCallerIdentityCommand } from "@aws-sdk/client-sts";
import { BedrockClient, ListInferenceProfilesCommand } from "@aws-sdk/client-bedrock";
import { getContextMap, getSystemPrompt } from "../lib/prompts.js";

export async function doctor() {
  const report = { embeddedPrompts: {}, aws: {}, errors: [] };

  // Check prompts
  try {
    const ctx = getContextMap();
    const sys = getSystemPrompt();
    report.embeddedPrompts = {
      OK: true,
      HasRevised: !!sys,
      ContextRows: ctx.size,
    };
  } catch (err) {
    report.embeddedPrompts = { OK: false };
    report.errors.push(`Prompts: ${err.message}`);
  }

  // Check AWS credentials
  const region = process.env.AWS_REGION || "us-east-1";
  try {
    const sts = new STSClient({ region });
    const identity = await sts.send(new GetCallerIdentityCommand({}));
    report.aws = {
      Region: region,
      Profile: process.env.AWS_PROFILE || "(default chain)",
      Identity: {
        Account: identity.Account,
        ARN: identity.Arn,
      },
    };
  } catch (err) {
    report.aws = { Region: region, Error: err.message };
    report.errors.push(`AWS credentials expired or not configured. Refresh and retry — no server restart needed.`);
  }

  // Check Bedrock access
  try {
    const bedrock = new BedrockClient({ region });
    const profiles = await bedrock.send(new ListInferenceProfilesCommand({}));
    const count = profiles.inferenceProfileSummaries?.length || 0;
    const hasModel = profiles.inferenceProfileSummaries?.some(
      (p) => p.inferenceProfileId?.includes("global.anthropic.claude-sonnet-4-5")
    );
    report.aws.Bedrock = { InferenceProfileCount: count, HasModelMatch: !!hasModel };
  } catch (err) {
    report.aws.Bedrock = { Error: err.message };
    if (!report.errors.length) report.errors.push(`Bedrock: ${err.message}`);
  }

  return report;
}
