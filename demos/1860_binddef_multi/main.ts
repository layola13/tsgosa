interface Pt { x: i32; y: i32; }
function main(): i32 {
  const o: Pt = {x: 5, y: 6};
  const {x = 9, y = 10} = o;
  console.log(x + y);
  return 0;
}
