interface N { id: i32; }
function get(n: N): i32 { return n.id; }
function main(): i32 {
  const o: N = { id: 41 };
  console.log(get(o) + 1);
  return 0;
}
