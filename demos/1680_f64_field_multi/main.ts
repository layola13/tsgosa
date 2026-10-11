class A { x: f64 = 1.5; y: f64 = 2.5; }
function main(): i32 {
  const a = new A();
  console.log(a.x + a.y);
  console.log(a.y);
  return 0;
}
