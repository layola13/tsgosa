class A { x: i32 = 1; }
class B extends A { y: i32 = 2; }
function main(): i32 {
  const a = new A();
  const b = new B();
  console.log(a instanceof A ? 1 : 0);
  console.log(b instanceof A ? 1 : 0);
  console.log(a instanceof B ? 1 : 0);
  return 0;
}
