interface Q { v: i32; }
interface P { q: Q; }
function main(): i32 {
  const o: P = { q: { v: 9 } };
  console.log(o.q.v);
  return 0;
}
