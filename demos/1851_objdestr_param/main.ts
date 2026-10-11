interface Pt { x: i32; }
function get(o: Pt): i32 {
  const {x} = o;
  return x;
}
function main(): i32 {
  const o: Pt = {x: 7};
  console.log(get(o));
  return 0;
}
