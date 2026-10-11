class A { x: f64 = 1.5; }
function main(): i32 {
  const a = new A();
  console.log(a.x + 2.5);
  console.log(a.x > 1 ? 1 : 0);
  return 0;
}
