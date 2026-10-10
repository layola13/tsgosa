function n(): i32 { return 4; }
function main(): i32 {
  let t = 0;
  for (let i = 0; i < n(); i++) { t += i; }
  console.log(t);
  return 0;
}
