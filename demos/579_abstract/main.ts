abstract class B { abstract g(): i32; }
class D extends B { g(): i32 { return 4; } }
function main(): i32 {
  const d: B = new D();
  console.log(d.g());
  return 0;
}
