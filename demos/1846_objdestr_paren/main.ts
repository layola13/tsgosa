interface Pt { x: i32; y: i32; }
function main(): i32 {
  const {x, y} = ({x: 3, y: 4} as Pt);
  console.log(x + y);
  return 0;
}
