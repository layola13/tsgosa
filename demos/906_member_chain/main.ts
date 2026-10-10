interface P { x: i32; }
function get(p: P): i32 { return p.x; }
function main(): i32 {
  const o: P = { x: 5 };
  console.log(get(o));
  return 0;
}
