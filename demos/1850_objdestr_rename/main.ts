interface Pt { x: i32; y: i32; }
function main(): i32 {
  const o: Pt = {x: 5, y: 6};
  const {x: a, y: b} = o;
  console.log(a + b);
  return 0;
}
