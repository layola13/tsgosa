interface D { d: i32; }
function bump(o: D): i32 { return o.d; }
function main(): i32 {
  const q: D = { d: 8 };
  console.log(bump(q) * 2);
  return 0;
}
