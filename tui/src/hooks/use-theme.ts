import {useEffect,useSyncExternalStore} from 'react';
import type {Theme} from '../themes/definitions.ts';
import type {BackendClient} from '../backend/client.ts';
import {themeCatalog} from '../themes/catalog.ts';
import {sharedThemeStore} from '../themes/store.ts';
export interface UseThemeResult {theme:Theme;themeName:string;setTheme:(name:string)=>Promise<void>;availableThemes:Array<{name:string;label:string}>;themeError:string|null;themePending:boolean}
export function useTheme(backend?:Pick<BackendClient,'call'>):UseThemeResult {
 const state=useSyncExternalStore(sharedThemeStore.subscribe,sharedThemeStore.getSnapshot);
 useEffect(()=>backend?sharedThemeStore.connect(backend):undefined,[backend]);
 return{theme:themeCatalog[state.themeName]!,themeName:state.themeName,setTheme:sharedThemeStore.setTheme,availableThemes:sharedThemeStore.availableThemes,themeError:state.error,themePending:state.pending};
}
