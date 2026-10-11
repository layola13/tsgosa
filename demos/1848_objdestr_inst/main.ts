class P { x: i32 = 0; y: i32 = 0; }
function main(): i32 {
  const p = new P();
  p.x = 3;
  p.y = 4;
  const {x, y} = p;
  console.log(x + y);
  return 0;
}
