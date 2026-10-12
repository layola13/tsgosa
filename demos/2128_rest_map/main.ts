interface P { x: number; y: number; }
function main(): i32 {
  const o: P = { x: 1, y: 2 };
  const { x, ...rest } = o;
  const m = new Map<string, number>();
  m.set("k", rest.y);
  console.log(m.get("k"));
  return 0;
}
