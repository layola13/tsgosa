interface Pt { x: i32; }
function get({x: a = 9}: Pt): i32 {
  return a;
}
function main(): i32 {
  const o: Pt = {x: 7};
  console.log(get(o));
  return 0;
}
