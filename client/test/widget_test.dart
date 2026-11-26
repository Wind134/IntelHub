import 'package:flutter_test/flutter_test.dart';

import 'package:intel_hub_client/main.dart';

void main() {
  testWidgets('IntelHubApp renders shell', (WidgetTester tester) async {
    await tester.pumpWidget(const IntelHubApp());
    expect(find.text('实时突发流'), findsOneWidget);
  });
}
