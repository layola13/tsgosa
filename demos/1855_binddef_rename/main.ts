interface Pt { x: i32; y: i32; }
function main(): i32 {
  const o: Pt = {x: 5, y: 6};
  const {x: a = 9} = o;
  console.log(a);
  return 0;
}
