enum D { Sun, Mon, Tue, Wed, Thu, Fri, Sat }
function isWeekend(d: D): i32 {
  if (d == D.Sun) {
    return 1;
  }
  if (d == D.Sat) {
    return 1;
  }
  return 0;
}
function main(): i32 {
  console.log(isWeekend(D.Sun), isWeekend(D.Mon), isWeekend(D.Sat));
  return 0;
}