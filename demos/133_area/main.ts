interface Rect { w: i32; h: i32; }
function area(r: Rect): i32 {
  return r.w * r.h;
}
function main(): i32 {
  const r: Rect = { w: 6, h: 7 };
  console.log(area(r));
  return 0;
}
