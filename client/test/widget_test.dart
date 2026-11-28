import 'package:flutter_test/flutter_test.dart';

import 'package:intel_hub_client/main.dart';

void main() {
  testWidgets('informationHub app renders cleanly', (WidgetTester tester) async {
    await tester.pumpWidget(const IntelHubApp());
    await tester.pump();
    expect(find.textContaining('informationHub'), findsWidgets);
  });
}
