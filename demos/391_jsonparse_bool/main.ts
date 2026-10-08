interface P {
  flag: boolean;
  n: i32;
}
function main(): i32 {
  const p: P = JSON.parse("{\"flag\":true,\"n\":2}");
  if (p.flag) { console.log(p.n + 10); } else { console.log(p.n); }
  return 0;
}
