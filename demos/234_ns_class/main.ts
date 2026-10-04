namespace N {
  export class C {
    v: i32 = 0;
    constructor(n: i32) {
      this.v = n;
    }
  }
}
function main(): i32 {
  const c = new N.C(41);
  return c.v;
}
console.log(main());
