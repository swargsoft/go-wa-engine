/**
 * React Hooks for WaEngine
 *
 * Provides convenient hooks for managing WhatsApp sessions in React components.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import WaEngine, {
  MessageReceivedData,
  QREventData,
  SessionInfo,
  WaEngineEvent,
  WaEngineEventType,
} from './WaEngine';

// ============================================================================
// useWaEngine - Main hook for session management
// ============================================================================

export interface UseWaEngineOptions {
  /** Session name to manage */
  sessionName: string;
  /** Auto-start session on mount if paired */
  autoStart?: boolean;
  /** Callback for all events */
  onEvent?: (event: WaEngineEvent) => void;
  /** Callback for QR code events */
  onQRCode?: (code: string) => void;
  /** Callback for pairing success */
  onPaired?: (jid: string) => void;
  /** Callback for connection state changes */
  onConnectionChange?: (connected: boolean) => void;
  /** Callback for received messages */
  onMessage?: (message: MessageReceivedData) => void;
}

export interface UseWaEngineReturn {
  /** Whether session is paired (has stored credentials) */
  isPaired: boolean;
  /** Whether session is currently connected */
  isConnected: boolean;
  /** Current QR code if in pairing mode */
  qrCode: string | null;
  /** Session JID if connected */
  jid: string | null;
  /** Loading state for async operations */
  isLoading: boolean;
  /** Last error if any */
  error: Error | null;
  /** Start the session (reconnect if paired) */
  start: () => Promise<void>;
  /** Start pairing (show QR code) */
  startPairing: () => Promise<void>;
  /** Stop the session */
  stop: () => Promise<void>;
  /** Send a text message */
  sendMessage: (to: string, message: string) => Promise<string>;
  /** Send image with caption */
  sendImage: (to: string, image: string, caption: string) => Promise<string>;
}

/**
 * Hook for managing a single WhatsApp session.
 *
 * @example
 * ```tsx
 * function ChatScreen() {
 *   const {
 *     isPaired,
 *     isConnected,
 *     qrCode,
 *     start,
 *     startPairing,
 *     sendMessage,
 *   } = useWaEngine({
 *     sessionName: 'work',
 *     autoStart: true,
 *     onMessage: (msg) => console.log('New message:', msg.text),
 *   });
 *
 *   if (qrCode) {
 *     return <QRCodeDisplay value={qrCode} />;
 *   }
 *
 *   if (!isConnected) {
 *     return <Button onPress={isPaired ? start : startPairing} title="Connect" />;
 *   }
 *
 *   return <ChatUI onSend={(text) => sendMessage(jid, text)} />;
 * }
 * ```
 */
export function useWaEngine(options: UseWaEngineOptions): UseWaEngineReturn {
  const {
    sessionName,
    autoStart = false,
    onEvent,
    onQRCode,
    onPaired,
    onConnectionChange,
    onMessage,
  } = options;

  const [isPaired, setIsPaired] = useState(false);
  const [isConnected, setIsConnected] = useState(false);
  const [qrCode, setQrCode] = useState<string | null>(null);
  const [jid, setJid] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  // Refs for callbacks to avoid stale closures
  const callbacksRef = useRef({
    onEvent,
    onQRCode,
    onPaired,
    onConnectionChange,
    onMessage,
  });
  callbacksRef.current = {
    onEvent,
    onQRCode,
    onPaired,
    onConnectionChange,
    onMessage,
  };

  // Check initial state
  useEffect(() => {
    const checkState = async () => {
      try {
        const paired = await WaEngine.isPaired(sessionName);
        const connected = await WaEngine.isConnected(sessionName);
        setIsPaired(paired);
        setIsConnected(connected);
        setIsLoading(false);

        // Auto-start if configured and paired
        if (autoStart && paired && !connected) {
          await WaEngine.startSession(sessionName);
        }
      } catch (e) {
        setError(e as Error);
        setIsLoading(false);
      }
    };
    checkState();
  }, [sessionName, autoStart]);

  // Event listener
  useEffect(() => {
    const subscription = WaEngine.addSessionListener(sessionName, (event) => {
      // Call generic event handler
      callbacksRef.current.onEvent?.(event);

      // Handle specific events
      switch (event.type) {
        case 'qr.updated':
          const code = (event.data as unknown as QREventData).code;
          setQrCode(code);
          callbacksRef.current.onQRCode?.(code);
          break;

        case 'qr.expired':
          setQrCode(null);
          break;

        case 'pairing.success':
          const pairingJid = (event.data as any).jid;
          setQrCode(null);
          setIsPaired(true);
          setJid(pairingJid);
          callbacksRef.current.onPaired?.(pairingJid);
          break;

        case 'connection.open':
          setIsConnected(true);
          callbacksRef.current.onConnectionChange?.(true);
          break;

        case 'connection.closed':
        case 'logged_out':
          setIsConnected(false);
          if (event.type === 'logged_out') {
            setIsPaired(false);
            setJid(null);
          }
          callbacksRef.current.onConnectionChange?.(false);
          break;

        case 'message.received':
          callbacksRef.current.onMessage?.(
            event.data as unknown as MessageReceivedData
          );
          break;
      }
    });

    return () => subscription.remove();
  }, [sessionName]);

  // Actions
  const start = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      await WaEngine.startSession(sessionName);
    } catch (e) {
      setError(e as Error);
      throw e;
    } finally {
      setIsLoading(false);
    }
  }, [sessionName]);

  const startPairing = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      await WaEngine.startPairing(sessionName);
    } catch (e) {
      setError(e as Error);
      throw e;
    } finally {
      setIsLoading(false);
    }
  }, [sessionName]);

  const stop = useCallback(async () => {
    setIsLoading(true);
    try {
      await WaEngine.stopSession(sessionName);
      setIsConnected(false);
      setQrCode(null);
    } catch (e) {
      setError(e as Error);
      throw e;
    } finally {
      setIsLoading(false);
    }
  }, [sessionName]);

  const sendMessage = useCallback(
    async (to: string, message: string) => {
      const result = await WaEngine.sendMessage(to, sessionName, message);
      return result.messageId;
    },
    [sessionName]
  );

  const sendImage = useCallback(
    async (to: string, image: string, caption: string) => {
      const result = await WaEngine.sendImageWithCaption(
        to,
        sessionName,
        image,
        caption
      );
      return result.messageId;
    },
    [sessionName]
  );

  return {
    isPaired,
    isConnected,
    qrCode,
    jid,
    isLoading,
    error,
    start,
    startPairing,
    stop,
    sendMessage,
    sendImage,
  };
}

// ============================================================================
// useWaEngineEvents - Hook for listening to events across all sessions
// ============================================================================

export interface UseWaEngineEventsOptions {
  /** Filter by event types */
  eventTypes?: WaEngineEventType[];
  /** Filter by session names */
  sessionNames?: string[];
}

/**
 * Hook for listening to WaEngine events across all sessions.
 *
 * @example
 * ```tsx
 * function NotificationListener() {
 *   const events = useWaEngineEvents({
 *     eventTypes: ['message.received'],
 *   });
 *
 *   // events is an array of recent events
 *   return (
 *     <View>
 *       {events.map((e, i) => (
 *         <Text key={i}>{e.sessionName}: {e.type}</Text>
 *       ))}
 *     </View>
 *   );
 * }
 * ```
 */
export function useWaEngineEvents(
  options: UseWaEngineEventsOptions = {},
  maxEvents: number = 50
): WaEngineEvent[] {
  const { eventTypes, sessionNames } = options;
  const [events, setEvents] = useState<WaEngineEvent[]>([]);

  useEffect(() => {
    const subscription = WaEngine.addListener((event) => {
      // Filter by event type if specified
      if (eventTypes && !eventTypes.includes(event.type)) {
        return;
      }

      // Filter by session name if specified
      if (sessionNames && !sessionNames.includes(event.sessionName)) {
        return;
      }

      setEvents((prev: WaEngineEvent[]) => {
        const updated = [event, ...prev];
        return updated.slice(0, maxEvents);
      });
    });

    return () => subscription.remove();
  }, [eventTypes?.join(','), sessionNames?.join(','), maxEvents]);

  return events;
}

// ============================================================================
// useWaEngineSessions - Hook for listing and monitoring all sessions
// ============================================================================

/**
 * Hook for monitoring all active sessions.
 *
 * @example
 * ```tsx
 * function SessionList() {
 *   const { sessions, refresh, isLoading } = useWaEngineSessions();
 *
 *   return (
 *     <FlatList
 *       data={sessions}
 *       renderItem={({ item }) => (
 *         <Text>{item.name}: {item.is_connected ? 'Online' : 'Offline'}</Text>
 *       )}
 *       onRefresh={refresh}
 *       refreshing={isLoading}
 *     />
 *   );
 * }
 * ```
 */
export function useWaEngineSessions(): {
  sessions: SessionInfo[];
  count: number;
  maxSessions: number;
  isLoading: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
} {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [count, setCount] = useState(0);
  const [maxSessions, setMaxSessions] = useState(5);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const refresh = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const info = await WaEngine.getAllSessionsInfo();
      setSessions(info.sessions);
      setCount(info.count);
      setMaxSessions(info.max_sessions);
    } catch (e) {
      setError(e as Error);
    } finally {
      setIsLoading(false);
    }
  }, []);

  // Initial load
  useEffect(() => {
    refresh();
  }, [refresh]);

  // Refresh on connection events
  useEffect(() => {
    const subscription = WaEngine.addListener((event) => {
      if (
        event.type === 'connection.open' ||
        event.type === 'connection.closed' ||
        event.type === 'pairing.success' ||
        event.type === 'logged_out'
      ) {
        refresh();
      }
    });

    return () => subscription.remove();
  }, [refresh]);

  return { sessions, count, maxSessions, isLoading, error, refresh };
}

// ============================================================================
// useWaEngineDebug - Hook for debugging event queue stats
// ============================================================================

export interface UseWaEngineDebugReturn {
  /** Event queue stats for the session */
  stats: {
    queueSize: number;
    totalEnqueued: number;
    totalPolled: number;
    dropped: number;
  } | null;
  /** Whether polling is currently active */
  isPolling: boolean;
  /** Whether app is in foreground */
  isAppForeground: boolean;
  /** Number of active polling tasks across all sessions */
  activePollingTasks: number;
  /** Last error if any */
  error: Error | null;
  /** Manual refresh function */
  refresh: () => Promise<void>;
}

/**
 * Hook for debugging event queue and polling status.
 * Useful during development to monitor SDK behavior.
 *
 * Connection-aware polling: The SDK only polls when isConnected() returns true.
 * This means polling won't waste battery when the session is disconnected.
 *
 * @param sessionName Session to monitor
 * @param refreshIntervalMs How often to refresh stats (0 = manual only)
 *
 * @example
 * ```tsx
 * function DebugPanel({ sessionName }: { sessionName: string }) {
 *   const { stats, isPolling, isAppForeground, refresh } = useWaEngineDebug(sessionName, 1000);
 *
 *   return (
 *     <View>
 *       <Text>Queue size: {stats?.queueSize ?? 'N/A'}</Text>
 *       <Text>Polling: {isPolling ? 'Yes' : 'No'}</Text>
 *       <Text>Foreground: {isAppForeground ? 'Yes' : 'No'}</Text>
 *       <Text>Dropped: {stats?.dropped ?? 0}</Text>
 *       <Button onPress={refresh} title="Refresh" />
 *     </View>
 *   );
 * }
 * ```
 */
export function useWaEngineDebug(
  sessionName: string,
  refreshIntervalMs: number = 0
): UseWaEngineDebugReturn {
  const [stats, setStats] = useState<UseWaEngineDebugReturn['stats']>(null);
  const [isPolling, setIsPolling] = useState(false);
  const [isAppForeground, setIsAppForeground] = useState(true);
  const [activePollingTasks, setActivePollingTasks] = useState(0);
  const [error, setError] = useState<Error | null>(null);

  const refresh = useCallback(async () => {
    try {
      const result = await WaEngine.getEventQueueStats(sessionName);
      setStats(result.queue);
      setIsPolling(result.isPolling);
      setIsAppForeground(result.isAppForeground);
      setActivePollingTasks(result.activePollingTasks);
      setError(null);
    } catch (e) {
      setError(e as Error);
    }
  }, [sessionName]);

  // Initial load
  useEffect(() => {
    refresh();
  }, [refresh]);

  // Auto-refresh if interval is set
  useEffect(() => {
    if (refreshIntervalMs <= 0) return;

    const interval = setInterval(refresh, refreshIntervalMs);
    return () => clearInterval(interval);
  }, [refresh, refreshIntervalMs]);

  return {
    stats,
    isPolling,
    isAppForeground,
    activePollingTasks,
    error,
    refresh,
  };
}
