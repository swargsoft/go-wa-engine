package com.waengine

import com.facebook.react.ReactPackage
import com.facebook.react.bridge.NativeModule
import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.uimanager.ViewManager

/**
 * WaEnginePackage - Registers the WaEngineModule with React Native
 *
 * INSTALLATION:
 * Add this package to your MainApplication.java/kt:
 *
 * ```kotlin
 * override fun getPackages(): List<ReactPackage> = listOf(
 *     MainReactPackage(),
 *     WaEnginePackage()  // Add this
 * )
 * ```
 *
 * Or in Java:
 * ```java
 * @Override
 * protected List<ReactPackage> getPackages() {
 *     return Arrays.asList(
 *         new MainReactPackage(),
 *         new WaEnginePackage()  // Add this
 *     );
 * }
 * ```
 */
class WaEnginePackage : ReactPackage {

    /**
     * Creates and returns the WaEngineModule instance.
     * Called by React Native during initialization.
     */
    override fun createNativeModules(reactContext: ReactApplicationContext): List<NativeModule> {
        return listOf(WaEngineModule(reactContext))
    }

    /**
     * No custom view managers for this module.
     */
    override fun createViewManagers(reactContext: ReactApplicationContext): List<ViewManager<*, *>> {
        return emptyList()
    }
}
