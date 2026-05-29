/**
 * Bedrock Converse API wrapper.
 */

import {
  BedrockRuntimeClient,
  ConverseCommand,
} from "@aws-sdk/client-bedrock-runtime";

const DEFAULT_MODEL = "global.anthropic.claude-sonnet-4-5-20250929-v1:0";
const MAX_TOKENS = 2000;

function getClient() {
  // Create fresh client each call so credential refresh is picked up
  return new BedrockRuntimeClient({
    region: process.env.AWS_REGION || "us-east-1",
  });
}

/**
 * Call Bedrock Converse with a system prompt and user message content blocks.
 */
export async function converse(systemPrompt, contentBlocks, modelId) {
  const client = getClient();
  const command = new ConverseCommand({
    modelId: modelId || DEFAULT_MODEL,
    system: [{ text: systemPrompt }],
    messages: [{ role: "user", content: contentBlocks }],
    inferenceConfig: { maxTokens: MAX_TOKENS, temperature: 0.0 },
  });

  const response = await client.send(command);
  const output = response.output?.message?.content?.[0]?.text || "";
  return {
    text: output,
    inputTokens: response.usage?.inputTokens || 0,
    outputTokens: response.usage?.outputTokens || 0,
  };
}
