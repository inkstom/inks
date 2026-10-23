#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#include "_cgo_export.h"

@interface InksMenuTarget : NSObject
@end

@implementation InksMenuTarget
- (void)fire:(NSMenuItem *)item {
	NSEvent *ev = [NSApp currentEvent];
	// Triggered by its shortcut: hand the key to the window so the editor's own
	// key routing (chords, text fields, user bindings) decides what it means.
	if (ev.type == NSEventTypeKeyDown && item.keyEquivalent.length > 0 &&
	    [[ev.charactersIgnoringModifiers lowercaseString] isEqualToString:[item.keyEquivalent lowercaseString]]) {
		NSWindow *w = [NSApp keyWindow];
		NSResponder *r = w.firstResponder;
		if (r != nil && r != w && [r respondsToSelector:@selector(keyDown:)]) {
			[r keyDown:ev];
			return;
		}
	}
	inksMenuFire((int)item.tag);
}
@end

static InksMenuTarget *target;

static NSMenu *buildMenu(NSString *title, NSArray *items) {
	NSMenu *menu = [[NSMenu alloc] initWithTitle:title];
	menu.autoenablesItems = NO;
	for (NSDictionary *it in items) {
		if ([it[@"sep"] boolValue]) {
			[menu addItem:[NSMenuItem separatorItem]];
			continue;
		}
		NSString *t = it[@"title"] ?: @"";
		NSString *key = it[@"key"] ?: @"";
		NSMenuItem *mi = [[NSMenuItem alloc] initWithTitle:t action:nil keyEquivalent:key];
		mi.keyEquivalentModifierMask = [it[@"mods"] unsignedIntegerValue];
		NSString *sel = it[@"sel"];
		if (sel.length > 0) {
			mi.action = NSSelectorFromString(sel);
		} else if (it[@"id"]) {
			mi.action = @selector(fire:);
			mi.target = target;
			mi.tag = [it[@"id"] integerValue];
		}
		mi.enabled = ![it[@"disabled"] boolValue];
		NSArray *sub = it[@"sub"];
		NSString *role = it[@"role"];
		if (sub || role) {
			NSMenu *sm = buildMenu(t, sub ?: @[]);
			mi.submenu = sm;
			if ([role isEqualToString:@"services"]) {
				NSApp.servicesMenu = sm;
			} else if ([role isEqualToString:@"window"]) {
				NSApp.windowsMenu = sm;
			} else if ([role isEqualToString:@"help"]) {
				NSApp.helpMenu = sm;
			}
		}
		[menu addItem:mi];
	}
	return menu;
}

void inks_set_menu(const char *json, int len) {
	NSData *data = [NSData dataWithBytes:json length:len];
	dispatch_async(dispatch_get_main_queue(), ^{
		if (target == nil) {
			target = [InksMenuTarget new];
		}
		NSArray *menus = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
		if (![menus isKindOfClass:[NSArray class]]) {
			return;
		}
		NSMenu *bar = [[NSMenu alloc] initWithTitle:@""];
		for (NSDictionary *m in menus) {
			NSMenuItem *top = [[NSMenuItem alloc] initWithTitle:m[@"title"] ?: @"" action:nil keyEquivalent:@""];
			NSMenu *sm = buildMenu(m[@"title"] ?: @"", m[@"sub"] ?: @[]);
			top.submenu = sm;
			NSString *role = m[@"role"];
			if ([role isEqualToString:@"window"]) {
				NSApp.windowsMenu = sm;
			} else if ([role isEqualToString:@"help"]) {
				NSApp.helpMenu = sm;
			}
			[bar addItem:top];
		}
		NSApp.mainMenu = bar;
	});
}

static NSApplicationTerminateReply inksAppShouldTerminate(id self, SEL _cmd, NSApplication *app) {
	return inksShouldTerminate() ? NSTerminateNow : NSTerminateCancel;
}

void inks_install_terminate(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		id del = NSApp.delegate;
		if (del == nil) {
			return;
		}
		class_addMethod([del class], @selector(applicationShouldTerminate:),
		                (IMP)inksAppShouldTerminate, "Q@:@");
	});
}

static void dumpMenu(NSMutableString *out, NSMenu *menu, int depth) {
	for (NSMenuItem *mi in menu.itemArray) {
		if (mi.isSeparatorItem) {
			continue;
		}
		for (int i = 0; i < depth; i++) {
			[out appendString:@"  "];
		}
		[out appendString:mi.title];
		if (mi.keyEquivalent.length > 0) {
			NSEventModifierFlags m = mi.keyEquivalentModifierMask;
			[out appendFormat:@" [%@%@%@%@%@]", (m & NSEventModifierFlagControl) ? @"⌃" : @"", (m & NSEventModifierFlagOption) ? @"⌥" : @"",
			 (m & NSEventModifierFlagShift) ? @"⇧" : @"", (m & NSEventModifierFlagCommand) ? @"⌘" : @"", mi.keyEquivalent];
		}
		if (!mi.enabled) {
			[out appendString:@" (disabled)"];
		}
		[out appendString:@"\n"];
		if (mi.submenu) {
			dumpMenu(out, mi.submenu, depth + 1);
		}
	}
}

char *inks_dump_menu(void) {
	__block char *res = NULL;
	dispatch_sync(dispatch_get_main_queue(), ^{
		NSMutableString *out = [NSMutableString string];
		dumpMenu(out, NSApp.mainMenu, 0);
		res = strdup(out.UTF8String);
	});
	return res;
}

// Place a window so its title bar sits under the pointer (a torn-off tab).
// The pointer is read now, on the calling thread; CoreGraphics is safe here.
void inks_place_window_at_mouse(uintptr_t viewRef) {
	CGEventRef ev = CGEventCreate(NULL);
	CGPoint p = CGEventGetLocation(ev); // top-left origin, global
	CFRelease(ev);
	dispatch_async(dispatch_get_main_queue(), ^{
		NSView *view = (__bridge NSView *)(void *)viewRef;
		NSWindow *w = view.window;
		if (w == nil) {
			return;
		}
		CGFloat top = NSMaxY(NSScreen.screens.firstObject.frame);
		// A little left of the pointer, so it lands over the new window's tab.
		[w setFrameTopLeftPoint:NSMakePoint(p.x - 80, top - p.y + 14)];
		[w makeKeyAndOrderFront:nil];
	});
}

void inks_terminate(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp terminate:nil];
	});
}

static BOOL inksAppShouldHandleReopen(id self, SEL _cmd, NSApplication *app, BOOL hasVisibleWindows) {
	if (!hasVisibleWindows) {
		inksReopen();
	}
	return YES;
}

void inks_install_reopen(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		id del = NSApp.delegate;
		if (del != nil) {
			class_addMethod([del class], @selector(applicationShouldHandleReopen:hasVisibleWindows:),
			                (IMP)inksAppShouldHandleReopen, "c@:@c");
		}
	});
}

@interface InksPopupTarget : NSObject
@property int picked;
@end
@implementation InksPopupTarget
- (void)pick:(NSMenuItem *)item {
	self.picked = (int)item.tag;
}
@end

// Show a context menu at the pointer; report the chosen index (or -1).
void inks_popup(const char *json, int len, int token) {
	NSData *data = [NSData dataWithBytes:json length:len];
	dispatch_async(dispatch_get_main_queue(), ^{
		NSArray *items = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
		InksPopupTarget *t = [InksPopupTarget new];
		t.picked = -1;
		NSMenu *menu = [[NSMenu alloc] initWithTitle:@""];
		menu.autoenablesItems = NO;
		NSInteger i = 0;
		for (NSDictionary *it in items) {
			NSString *title = it[@"title"] ?: @"";
			if (title.length == 0) {
				[menu addItem:[NSMenuItem separatorItem]];
			} else {
				NSMenuItem *mi = [[NSMenuItem alloc] initWithTitle:title action:@selector(pick:) keyEquivalent:it[@"key"] ?: @""];
				mi.keyEquivalentModifierMask = [it[@"mods"] unsignedIntegerValue];
				mi.target = t;
				mi.tag = i;
				mi.enabled = ![it[@"disabled"] boolValue];
				[menu addItem:mi];
			}
			i++;
		}
		[menu popUpMenuPositioningItem:nil atLocation:[NSEvent mouseLocation] inView:nil];
		inksPopupDone(token, t.picked);
	});
}

static BOOL inksAcceptsFirstMouse(id self, SEL _cmd, NSEvent *event) {
	return YES;
}

// Let the first click in an inactive window also act (select or drag a tab,
// place the caret), like other editors, instead of only activating it.
void inks_accept_first_mouse(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		Class cls = NSClassFromString(@"GioView");
		if (cls != nil) {
			class_addMethod(cls, @selector(acceptsFirstMouse:), (IMP)inksAcceptsFirstMouse, "c@:@");
		}
	});
}

// inks has its own tabs: opt out of macOS window tabbing, which otherwise
// adds a second, title-only tab bar when the user prefers tabs ("Prefer
// tabs when opening documents: Always", or View › Show Tab Bar).
void inks_disable_window_tabbing(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow.allowsAutomaticWindowTabbing = NO;
	});
}

void inks_window_setup(uintptr_t viewRef, int debug) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSView *view = (__bridge NSView *)(void *)viewRef;
		NSWindow *w = view.window;
		if (w == nil) {
			return;
		}
		if (debug) {
			NSLog(@"[tabs] before: tabbingMode=%ld tabBarVisible=%d tabbedWindows=%lu", (long)w.tabbingMode, w.tabGroup.isTabBarVisible, (unsigned long)w.tabbedWindows.count);
		}
		w.tabbingMode = NSWindowTabbingModeDisallowed;
		if (w.tabGroup.isTabBarVisible) {
			[w toggleTabBar:nil];
		}
		if (debug) {
			NSLog(@"[tabs] after: tabbingMode=%ld tabBarVisible=%d", (long)w.tabbingMode, w.tabGroup.isTabBarVisible);
		}
	});
}
