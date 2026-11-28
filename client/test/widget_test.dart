import 'package:flutter_test/flutter_test.dart';

import 'package:intel_hub_client/main.dart';

void main() {
  testWidgets('informationHub app renders shell', (WidgetTester tester) async {
    await tester.pumpWidget(const IntelHubApp());
    expect(find.textContaining('实时突发'), findsWidgets);
  });
}
