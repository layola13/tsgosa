interface P { v: number; }
function main(): i32 {
  const d: P = { v: 3 };
  let p: P|null = { v: 4 };
  const q: P = p ?? d;
  console.log(q.v);
  return 0;
}
