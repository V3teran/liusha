/*
 * Copyright 2024 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package callbacks provides callback mechanisms for component execution in Eino.
//
// This package allows you to inject callback handlers at different stages of component execution,
// such as start, end, and error handling. It's particularly useful for implementing governance capabilities like logging, monitoring, and metrics collection.
//
// The package provides two ways to create callback handlers:
//
// 1. Create a callback handler using HandlerBuilder:
//
//	handler := callbacks.NewHandlerBuilder().
//		OnStart(func(ctx context.Context, info *RunInfo, input CallbackInput) context.Context {
//			// Handle component start
//			return ctx
//		}).
//		OnEnd(func(ctx context.Context, info *RunInfo, output CallbackOutput) context.Context {
//			// Handle component end
//			return ctx
//		}).
//		OnError(func(ctx context.Context, info *RunInfo, err error) context.Context {
//			// Handle component error
//			return ctx
//		}).
//		OnStartWithStreamInput(func(ctx context.Context, info *RunInfo, input *schema.StreamReader[CallbackInput]) context.Context {
//			// Handle component start with stream input
//			return ctx
//		}).
//		OnEndWithStreamOutput(func(ctx context.Context, info *RunInfo, output *schema.StreamReader[CallbackOutput]) context.Context {
//			// Handle component end with stream output
//			return ctx
//		}).
//		Build()
//
// For this way, you need to convert the callback input types by yourself, and implement the logic for different component types in one handler.
//
// 2. Use [template.HandlerHelper] to create a handler:
//
// Package utils/callbacks provides [HandlerHelper] as a convenient way to build callback handlers
// for different component types. It allows you to set specific handlers for each component type,
//
// e.g.
//
//	// Create handlers for specific components
//	modelHandler := &model.CallbackHandler{
//		OnStart: func(ctx context.Context, info *RunInfo, input *model.CallbackInput) context.Context {
//			log.Printf("Model execution started: %s", info.ComponentName)
//			return ctx
//		},
//	}
//
//	promptHandler := &prompt.CallbackHandler{
//		OnEnd: func(ctx context.Context, info *RunInfo, output *prompt.CallbackOutput) context.Context {
//			log.Printf("Prompt execution completed: %s", output.Result)
//			return ctx
//		},
//	}
//
//	// Build the handler using HandlerHelper
//	handler := callbacks.NewHandlerHelper().
//		ChatModel(modelHandler).
//		Prompt(promptHandler).
//		Fallback(fallbackHandler).
//		Handler()
//
// [HandlerHelper] supports handlers for various component types including:
//   - Prompt components (via prompt.CallbackHandler)
//   - Chat model components (via model.CallbackHandler)
//   - Embedding components (via embedding.CallbackHandler)
//   - Indexer components (via indexer.CallbackHandler)
//   - Retriever components (via retriever.CallbackHandler)
//   - Document loader components (via loader.CallbackHandler)
//   - Document transformer components (via transformer.CallbackHandler)
//   - Tool components (via tool.CallbackHandler)
//   - Graph (via Handler)
//   - Chain (via Handler)
//   - Tools node (via Handler)
//   - Lambda (via Handler)
//
// Use the handler with a component:
//
//	runnable.Invoke(ctx, input, compose.WithCallbacks(handler))
package callbacks
