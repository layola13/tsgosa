import { z } from "zod";

// 1. primitives + coerce + brand
const BrandedString = z.string().brand<"UserId">();
const primitives = z.object({
  s: z.string().min(1).max(100).email().url().uuid().regex(/^[a-z]+$/).trim().toLowerCase(),
  s2: z.coerce.string(),
  n: z.number().int().positive().finite().safe().gt(0).lt(1000).multipleOf(2).default(42),
  n2: z.coerce.number(),
  b: z.boolean().default(false),
  bi: z.bigint(),
  sym: z.symbol(),
  d: z.date().min(new Date("2020-01-01")),
  undef: z.undefined(),
  nul: z.null(),
  void_: z.void(),
  any_: z.any(),
  unknown_: z.unknown(),
  never_: z.never(),
  lit: z.literal("hi" as const),
  lit2: z.literal(123),
});

// 2. enum / nativeEnum / literal union
enum NativeRole { Admin = "admin", User = "user" }
const enums = z.object({
  role: z.enum(["admin", "user", "guest"]),
  native: z.nativeEnum(NativeRole),
  unionLit: z.union([z.literal("a"), z.literal("b"), z.literal("c")]),
  discriminated: z.discriminatedUnion("type", [
    z.object({ type: z.literal("a"), a: z.string() }),
    z.object({ type: z.literal("b"), b: z.number() }),
  ]),
});

// 3. array / tuple / set / map / record / object configs
const collections = z.object({
  arr: z.array(z.string()).min(1).max(10).nonempty(),
  tuple: z.tuple([z.string(), z.number(), z.boolean()]).rest(z.string()),
  set: z.set(z.string()),
  map: z.map(z.string(), z.number()),
  record: z.record(z.string(), z.number()),
  strictObj: z.object({ a: z.string() }).strict(),
  passthroughObj: z.object({ a: z.string() }).passthrough(),
  stripObj: z.object({ a: z.string() }).strip(),
  catchallObj: z.object({ a: z.string() }).catchall(z.number()),
  partialObj: z.object({ a: z.string(), b: z.number() }).partial(),
  deepPartial: z.object({ a: z.object({ b: z.string() }) }).deepPartial(),
  requiredObj: z.object({ a: z.string().optional() }).required(),
  pickOmit: z.object({ a: z.string(), b: z.number(), c: z.boolean() }).pick({ a: true, b: true }).omit({ b: true }),
  extendMerge: z.object({ a: z.string() }).extend({ b: z.number() }).merge(z.object({ c: z.boolean() })),
});

// 4. union / intersection / optional / nullable / nullish / default / catch / transform / refine
const modifiers = z.object({
  union: z.union([z.string(), z.number()]),
  intersection: z.intersection(z.object({ a: z.string() }), z.object({ b: z.number() })),
  optional: z.string().optional(),
  nullable: z.string().nullable(),
  nullish: z.string().nullish(),
  defaulted: z.string().default("def"),
  catched: z.string().catch("catch"),
  transformed: z.string().transform(v => v.length).pipe(z.number()),
  coerced: z.coerce.string().transform(v => v.toUpperCase()),
  refined: z.string().refine(v => v.startsWith("a"), { message: "must start with a" }),
  superRefined: z.string().superRefine((v, ctx) => {
    if (v.length < 3) ctx.addIssue({ code: z.ZodIssueCode.too_small, minimum: 3, type: "string", inclusive: true, message: "too short" });
  }),
});

// 5. recursive / lazy / function / promise / lazy + recursive tree
type Tree = { value: string; children: Tree[] };
const TreeSchema: z.ZodType<Tree> = z.lazy(() => z.object({
  value: z.string(),
  children: z.array(TreeSchema),
}));

const func = z.object({
  fn: z.function().args(z.string(), z.number()).returns(z.boolean()),
  promise: z.promise(z.string()),
  lazyNum: z.lazy(() => z.number()),
  tree: TreeSchema,
});

// 6. THE BOSS: ZodObject with 5 type args + effects + preprocess + complex infer (Bun卡死的地方)
const ComplexZod = z.preprocess(
  (val) => typeof val === "string" ? JSON.parse(val as string) : val,
  z.object({
    id: z.string().uuid(),
    user: z.object({
      name: z.string(),
      age: z.number().optional(),
      tags: z.array(z.string()).default([]),
      meta: z.record(z.unknown()).optional(),
    }).transform(u => ({ ...u, displayName: `${u.name} (${u.age ?? 0})` })),
    posts: z.array(
      z.discriminatedUnion("kind", [
        z.object({ kind: z.literal("text"), text: z.string() }),
        z.object({ kind: z.literal("image"), url: z.string().url(), width: z.number(), height: z.number() }),
      ])
    ).min(1),
    status: z.enum(["draft", "published", "archived"]).default("draft"),
    extra: z.intersection(
      z.object({ createdAt: z.date() }),
      z.object({ updatedAt: z.date().optional() }).merge(z.record(z.any()))
    ).optional(),
  })
  .strict()
  .refine(d => d.posts.length > 0)
  .transform(d => ({ ...d, postCount: d.posts.length }))
);

// 7. final type inference + all-in-one
export const AllZodSchema = z.object({
  primitives,
  enums,
  collections,
  modifiers,
  func: func.shape,
  complex: ComplexZod,
  recursiveTree: TreeSchema.optional(),
}).strict();

export type AllZod = z.infer<typeof AllZodSchema>;
export type AllZodInput = z.input<typeof AllZodSchema>;
export type AllZodOutput = z.output<typeof AllZodSchema>;

export function testAll(): number {
  const res = AllZodSchema.safeParse({
    primitives: { s: "a@b.com", s2: 123, n: 2, n2: "123", b: true, bi: BigInt(1), sym: Symbol(), d: new Date(), undef: undefined, nul: null, void_: undefined, any_: 1, unknown_: 1, never_: undefined as never, lit: "hi", lit2: 123 },
    enums: { role: "admin", native: NativeRole.Admin, unionLit: "a", discriminated: { type: "a", a: "x" } },
    collections: { arr: ["a"], tuple: ["a", 1, true, "rest"], set: new Set(["a"]), map: new Map([["a", 1]]), record: { a: 1 }, strictObj: { a: "a" }, passthroughObj: { a: "a" }, stripObj: { a: "a" }, catchallObj: { a: "a", b: 1 }, partialObj: {}, deepPartial: {}, requiredObj: { a: "a" }, pickOmit: { a: "a" }, extendMerge: { a: "a", b: 1, c: true } },
    modifiers: { union: "a", intersection: { a: "a", b: 1 }, optional: undefined, nullable: null, nullish: null, defaulted: undefined, catched: undefined, transformed: "abc", coerced: 123, refined: "abc", superRefined: "abc" },
    func: { fn: () => true, promise: Promise.resolve("a"), lazyNum: 1, tree: { value: "root", children: [] } },
    complex: JSON.stringify({ id: "550e8400-e29b-40d4-a05e-3412-3412-3412-3412", user: { name: "hi" }, posts: [{ kind: "text", text: "hi" }], status: "draft", extra: { createdAt: new Date() } }),
    recursiveTree: { value: "root", children: [{ value: "child", children: [] }] }
  });
  return res.success ? 1 : 0;
}
