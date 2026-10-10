const C = class { x: i32 = 1; };
function main(): i32 {
  const c = new C();
  console.log(c.x);
  return 0;
}
