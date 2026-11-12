/**
 * Shared child-output parsing and final-output extraction (phase 3).
 *
 * Both the foreground `subagent` path (streams `pi --mode json` stdout live) and
 * the `job_status` tool (parses a finished job's `<id>.jsonl` from disk) must
 * agree on what a child's "final assistant text" is, so the message-parsing and
 * output-extraction logic lives here instead of in index.ts.
 */

import type { Message } from "@earendil-works/pi-ai";
import type { JsonAgentSessionEvent } from "@earendil-works/pi-coding-agent";

/** Output cap per job/task result — keeps parent context small (~50 KB). */
export const PER_TASK_OUTPUT_CAP = 50 * 1024;

export interface UsageStats {
	input: number;
	output: number;
	cacheRead: number;
	cacheWrite: number;
	cost: number;
	contextTokens: number;
	turns: number;
}

export function emptyUsage(): UsageStats {
	return { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0, contextTokens: 0, turns: 0 };
}

export interface ParsedChildStream {
	messages: Message[];
	usage: UsageStats;
	model?: string;
	stopReason?: string;
	errorMessage?: string;
}

export interface ChildStreamParser extends ParsedChildStream {
	/** Parse one JSONL line. Returns true when a message was appended. */
	processLine(line: string): boolean;
}

/**
 * Incremental parser for a child's JSON event stream (`pi --mode json` stdout).
 *
 * Mirrors the phase-1/2 inline logic: only `message_end` events for
 * user/assistant/toolResult messages are kept; assistant messages accumulate
 * usage and carry model/stopReason/errorMessage.
 */
export function createChildStreamParser(): ChildStreamParser {
	const parser: ChildStreamParser = {
		messages: [],
		usage: emptyUsage(),
		model: undefined,
		stopReason: undefined,
		errorMessage: undefined,
		processLine(line: string): boolean {
			if (!line.trim()) return false;
			let event: JsonAgentSessionEvent;
			try {
				event = JSON.parse(line) as JsonAgentSessionEvent;
			} catch {
				return false;
			}
			if (event.type !== "message_end") return false;
			const eventMessage = (event as { message?: unknown }).message;
			if (!eventMessage || typeof eventMessage !== "object") return false;
			const role = (eventMessage as { role?: unknown }).role;
			if (role !== "user" && role !== "assistant" && role !== "toolResult") return false;
			const message = eventMessage as Message;
			parser.messages.push(message);
			if (message.role === "assistant") {
				parser.usage.turns++;
				parser.usage.input += message.usage.input;
				parser.usage.output += message.usage.output;
				parser.usage.cacheRead += message.usage.cacheRead;
				parser.usage.cacheWrite += message.usage.cacheWrite;
				parser.usage.cost += message.usage.cost.total;
				parser.usage.contextTokens = message.usage.totalTokens;
				if (!parser.model) parser.model = message.model;
				parser.stopReason = message.stopReason;
				parser.errorMessage = message.errorMessage;
			}
			return true;
		},
	};
	return parser;
}

/** Parse a complete JSONL text (e.g. a finished job's `<id>.jsonl` file). */
export function parseChildJsonl(text: string): ParsedChildStream {
	const parser = createChildStreamParser();
	for (const line of text.split("\n")) parser.processLine(line);
	return {
		messages: parser.messages,
		usage: parser.usage,
		model: parser.model,
		stopReason: parser.stopReason,
		errorMessage: parser.errorMessage,
	};
}

/** Last assistant text in the message list (what the child "returned"). */
export function getFinalOutput(messages: Message[]): string {
	for (let i = messages.length - 1; i >= 0; i--) {
		const msg = messages[i];
		if (msg.role === "assistant") {
			for (const part of msg.content) {
				if (part.type === "text") return part.text;
			}
		}
	}
	return "";
}

/**
 * Byte-safe truncation to PER_TASK_OUTPUT_CAP, same semantics as phase 1/2:
 * never splits a UTF-8 codepoint, appends an omission notice with the byte count.
 */
export function truncateParallelOutput(output: string): string {
	const byteLength = Buffer.byteLength(output, "utf8");
	if (byteLength <= PER_TASK_OUTPUT_CAP) return output;

	let truncated = output.slice(0, PER_TASK_OUTPUT_CAP);
	while (Buffer.byteLength(truncated, "utf8") > PER_TASK_OUTPUT_CAP) {
		truncated = truncated.slice(0, -1);
	}
	const omitted = byteLength - Buffer.byteLength(truncated, "utf8");
	return truncated + "\n\n[Output truncated: " + omitted + " bytes omitted. Full output preserved in tool details.]";
}
