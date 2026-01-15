/**
 * WaEngine - React Native WhatsApp SDK Bridge
 *
 * TypeScript type definitions and JS wrapper for the native module.
 * Provides a clean API for multi-session WhatsApp functionality.
 */

import {
  EmitterSubscription,
  NativeEventEmitter,
  NativeModules,
} from 'react-native';

// ============================================================================
// TYPE DEFINITIONS
// ============================================================================

/**
 * Event payload emitted from native module.
 * All events include sessionName for routing.
 */
export interface WaEngineEvent {
  /** Session that generated this event */
  sessionName: string;
  /** Event type (e.g., "qr.updated", "message.received") */
  type: WaEngineEventType;
  /** Event-specific data */
  data: Record<string, unknown>;
  /** Unix timestamp in milliseconds */
  timestamp?: number;
}

/**
 * All possible event types from the SDK.
 */
export type WaEngineEventType =
  // QR & Pairing
  | 'qr.updated'
  | 'qr.expired'
  | 'pairing.success'
  | 'pairing.failed'
  // Connection
  | 'connection.open'
  | 'connection.closed'
  | 'connection.reconnecting'
  // Authentication
  | 'logged_in'
  | 'logged_out'
  // Messages
  | 'message.received'
  | 'message.sent'
  | 'message.failed'
  // System
  | 'event.dropped'
  | 'error'
  | 'unknown';

/**
 * QR code event data.
 */
export interface QREventData {
  code: string;
}

/**
 * Pairing success event data.
 */
export interface PairingSuccessData {
  jid: string;
}

/**
 * Pairing failed event data.
 */
export interface PairingFailedData {
  reason: string;
}

/**
 * Message received event data.
 */
export interface MessageReceivedData {
  id: string;
  chat_jid: string;
  sender_jid: string;
  sender_name?: string;
  text?: string;
  timestamp: number;
  is_from_me: boolean;
  is_group: boolean;
  quoted_id?: string;
  quoted_text?: string;
  has_media: boolean;
  media_type?: string;
  media_mime_type?: string;
}

/**
 * Message sent/failed event data.
 */
export interface MessageSentData {
  id: string;
  chat_jid?: string;
}

export interface MessageFailedData {
  to: string;
  error: string;
  code: string;
}

/**
 * Session info returned by getAllSessionsInfo().
 */
export interface SessionInfo {
  name: string;
  is_paired: boolean;
  is_connected: boolean;
  state: string;
  connection_state: string;
  jid?: string;
  event_queue_size: number;
}

/**
 * Result from getAllSessionsInfo().
 */
export interface AllSessionsInfo {
  sessions: SessionInfo[];
  count: number;
  max_sessions: number;
}

/**
 * Result from sendMessage/sendImageWithCaption.
 */
export interface SendResult {
  messageId: string;
}

/**
 * Event queue statistics from getEventQueueStats().
 * Useful for debugging polling behavior.
 */
export interface EventQueueStats {
  /** Stats from Go SDK event queue */
  queue: {
    queueSize: number;
    totalEnqueued: number;
    totalPolled: number;
    dropped: number;
  };
  /** Whether polling is currently active for this session */
  isPolling: boolean;
  /** Whether app is in foreground (polling only runs in foreground) */
  isAppForeground: boolean;
  /** Total number of sessions with active polling tasks */
  activePollingTasks: number;
}

// ============================================================================
// NATIVE MODULE INTERFACE
// ============================================================================

interface WaEngineNativeModule {
  init(dataDir: string): Promise<void>;
  startSession(sessionName: string): Promise<void>;
  startPairing(sessionName: string): Promise<void>;
  stopSession(sessionName: string): Promise<void>;
  stopAll(): Promise<void>;
  isPaired(sessionName: string): Promise<boolean>;
  isConnected(sessionName: string): Promise<boolean>;
  listSessions(): Promise<string[]>;
  getAllSessionsInfo(): Promise<AllSessionsInfo>;
  getEventQueueStats(sessionName: string): Promise<EventQueueStats>;
  sendMessage(
    to: string,
    sessionName: string,
    message: string
  ): Promise<SendResult>;
  sendImageWithCaption(
    to: string,
    sessionName: string,
    image: string,
    caption: string
  ): Promise<SendResult>;
  // Event listener methods (required for NativeEventEmitter)
  addListener(eventName: string): void;
  removeListeners(count: number): void;
}

// ============================================================================
// MODULE ACCESS
// ============================================================================

const { WaEngineModule } = NativeModules;

if (!WaEngineModule) {
  throw new Error(
    'WaEngineModule not found. Did you forget to link the native module?\n' +
      'Make sure to:\n' +
      '1. Add WaEnginePackage to your MainApplication\n' +
      '2. Rebuild the app (npx react-native run-android)'
  );
}

const nativeModule = WaEngineModule as WaEngineNativeModule;
const eventEmitter = new NativeEventEmitter(WaEngineModule);

// ============================================================================
// PUBLIC API
// ============================================================================

/**
 * WaEngine - Multi-session WhatsApp SDK for React Native
 *
 * @example
 * ```typescript
 * import WaEngine from './WaEngine';
 *
 * // Initialize once on app start
 * await WaEngine.init('/path/to/data');
 *
 * // Start a session
 * if (await WaEngine.isPaired('work')) {
 *   await WaEngine.startSession('work');
 * } else {
 *   await WaEngine.startPairing('work');
 * }
 *
 * // Listen for events
 * const subscription = WaEngine.addListener((event) => {
 *   console.log(`[${event.sessionName}] ${event.type}:`, event.data);
 * });
 *
 * // Send a message
 * await WaEngine.sendMessage('1234567890@s.whatsapp.net', 'work', 'Hello!');
 *
 * // Cleanup
 * subscription.remove();
 * await WaEngine.stopAll();
 * ```
 */
const WaEngine = {
  /**
   * Initialize the WhatsApp engine.
   * Must be called before any other method.
   *
   * @param dataDir App-private directory for session databases
   */
  async init(dataDir: string): Promise<void> {
    return nativeModule.init(dataDir);
  },

  /**
   * Start a session (reconnects if already paired).
   * Automatically starts event polling for this session.
   *
   * @param sessionName Unique identifier for this WhatsApp account
   */
  async startSession(sessionName: string): Promise<void> {
    return nativeModule.startSession(sessionName);
  },

  /**
   * Start QR pairing for a new session.
   * Listen for 'qr.updated' events to display QR code.
   *
   * @param sessionName Unique identifier for this WhatsApp account
   */
  async startPairing(sessionName: string): Promise<void> {
    return nativeModule.startPairing(sessionName);
  },

  /**
   * Stop a session and its event polling.
   *
   * @param sessionName Session to stop
   */
  async stopSession(sessionName: string): Promise<void> {
    return nativeModule.stopSession(sessionName);
  },

  /**
   * Stop all sessions and all event polling.
   * Call this before app termination.
   */
  async stopAll(): Promise<void> {
    return nativeModule.stopAll();
  },

  /**
   * Check if a session has stored credentials.
   *
   * @param sessionName Session to check
   * @returns true if paired with WhatsApp
   */
  async isPaired(sessionName: string): Promise<boolean> {
    return nativeModule.isPaired(sessionName);
  },

  /**
   * Check if a session is currently connected.
   *
   * @param sessionName Session to check
   * @returns true if connected to WhatsApp servers
   */
  async isConnected(sessionName: string): Promise<boolean> {
    return nativeModule.isConnected(sessionName);
  },

  /**
   * Get list of all session names.
   *
   * @returns Array of session names
   */
  async listSessions(): Promise<string[]> {
    return nativeModule.listSessions();
  },

  /**
   * Get detailed info for all sessions.
   *
   * @returns Session info object
   */
  async getAllSessionsInfo(): Promise<AllSessionsInfo> {
    return nativeModule.getAllSessionsInfo();
  },

  /**
   * Get event queue statistics for a session.
   * Useful for debugging polling behavior and diagnosing event issues.
   *
   * Connection-aware polling: Events are only polled when isConnected() is true.
   * This saves battery by avoiding unnecessary poll calls.
   *
   * @param sessionName Session to get stats for
   * @returns Queue stats including native-side polling info
   *
   * @example
   * ```typescript
   * const stats = await WaEngine.getEventQueueStats('work');
   * console.log('Queue size:', stats.queue.queueSize);
   * console.log('Is polling:', stats.isPolling);
   * console.log('App foreground:', stats.isAppForeground);
   * ```
   */
  async getEventQueueStats(sessionName: string): Promise<EventQueueStats> {
    return nativeModule.getEventQueueStats(sessionName);
  },

  /**
   * Send a text message via a specific session.
   *
   * @param to Recipient JID (e.g., "1234567890@s.whatsapp.net")
   * @param sessionName Which account to send from
   * @param message Text content
   * @returns Message ID
   */
  async sendMessage(
    to: string,
    sessionName: string,
    message: string
  ): Promise<SendResult> {
    return nativeModule.sendMessage(to, sessionName, message);
  },

  /**
   * Send an image with caption via a specific session.
   *
   * @param to Recipient JID
   * @param sessionName Which account to send from
   * @param image URL or base64 data URI
   * @param caption Caption text
   * @returns Message ID
   */
  async sendImageWithCaption(
    to: string,
    sessionName: string,
    image: string,
    caption: string
  ): Promise<SendResult> {
    return nativeModule.sendImageWithCaption(to, sessionName, image, caption);
  },

  /**
   * Add a listener for WhatsApp events.
   * Events include sessionName for routing to the correct handler.
   *
   * @param callback Function to call when event is received
   * @returns Subscription object with remove() method
   *
   * @example
   * ```typescript
   * const sub = WaEngine.addListener((event) => {
   *   if (event.sessionName === 'work') {
   *     switch (event.type) {
   *       case 'qr.updated':
   *         showQRCode((event.data as QREventData).code);
   *         break;
   *       case 'message.received':
   *         handleMessage(event.data as MessageReceivedData);
   *         break;
   *     }
   *   }
   * });
   *
   * // Later: cleanup
   * sub.remove();
   * ```
   */
  addListener(callback: (event: WaEngineEvent) => void): EmitterSubscription {
    return eventEmitter.addListener('wa-engine-event', callback);
  },

  /**
   * Add a listener for a specific session only.
   * Convenience wrapper that filters events by sessionName.
   *
   * @param sessionName Session to listen for
   * @param callback Function to call when event is received
   * @returns Subscription object with remove() method
   */
  addSessionListener(
    sessionName: string,
    callback: (event: WaEngineEvent) => void
  ): EmitterSubscription {
    return eventEmitter.addListener(
      'wa-engine-event',
      (event: WaEngineEvent) => {
        if (event.sessionName === sessionName) {
          callback(event);
        }
      }
    );
  },

  /**
   * Helper to format a phone number to WhatsApp JID.
   *
   * @param phone Phone number with country code (no + or 00)
   * @returns JID string
   */
  formatPhoneJID(phone: string): string {
    // Remove any non-digit characters
    const cleaned = phone.replace(/\D/g, '');
    return `${cleaned}@s.whatsapp.net`;
  },

  /**
   * Helper to format a group ID to WhatsApp JID.
   *
   * @param groupId Group ID (format: xxx-xxx)
   * @returns JID string
   */
  formatGroupJID(groupId: string): string {
    return `${groupId}@g.us`;
  },
};

export default WaEngine;

// Also export types for consumers
export type { EmitterSubscription };
